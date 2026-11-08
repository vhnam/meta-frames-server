package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// callWith sends a JSON request with extra headers, signed in as "tester".
func (harness *harness) callWith(method, path string, headers map[string]string, body any) response {
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	return harness.serve(request)
}

// auditCount counts the audit rows of one entity.
func (harness *harness) auditCount(entityID string) int {
	harness.test.Helper()
	var count int
	if err := harness.pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE entity_id = $1", entityID).Scan(&count); err != nil {
		harness.test.Fatal(err)
	}
	return count
}

func (harness *harness) rollOf(rollID string) map[string]any {
	harness.test.Helper()
	return asObject(harness.expect(harness.call("GET", "/rolls/"+rollID, nil), http.StatusOK).Body["roll"])
}

func versionOf(roll map[string]any) int { return int(roll["version"].(float64)) }

func ifMatch(version int) map[string]string {
	return map[string]string{"If-Match": fmt.Sprintf("%q", fmt.Sprint(version))}
}

// race sends the same number of requests at once and counts the statuses.
func race(count int, send func(index int) response) map[int]int {
	statuses := map[int]int{}
	var lock sync.Mutex
	var group sync.WaitGroup
	start := make(chan struct{})
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result := send(index)
			lock.Lock()
			statuses[result.Status]++
			lock.Unlock()
		}()
	}
	close(start)
	group.Wait()
	return statuses
}

func TestRollVersionGuardsTheFullEditAgainstMobileNotes(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	stockEdit := func(description string) map[string]any {
		return map[string]any{"filmStockId": seeded.stockID, "format": 135, "exposures": 36, "description": description}
	}

	created := versionOf(harness.rollOf(rollID))
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/load", map[string]any{"cameraId": seeded.slrCamera, "startedAt": "2026-09-01"}), http.StatusOK)
	loaded := versionOf(harness.rollOf(rollID))
	if loaded <= created {
		test.Fatalf("load must move the version: %d -> %d", created, loaded)
	}

	// Ordering 1: the mobile note lands, then a dashboard form opened before it saves.
	noted := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/notes", map[string]any{"description": "from the phone", "baseDescription": ""}), http.StatusOK)
	if versionOf(asObject(noted.Body["roll"])) <= loaded {
		test.Fatalf("notes must move the version: %s", noted.Raw)
	}
	refused := harness.expect(harness.callWith("PUT", "/rolls/"+rollID, ifMatch(loaded), stockEdit("stale form")), http.StatusPreconditionFailed)
	if refused.Body["code"] != "roll_changed" {
		test.Fatalf("412 body = %s", refused.Raw)
	}
	if roll := harness.rollOf(rollID); roll["description"] != "from the phone" {
		test.Fatalf("the stale edit overwrote the note: %v", roll)
	}

	// A replayed note writes nothing: same version, no audit row.
	current, audited := versionOf(harness.rollOf(rollID)), harness.auditCount(rollID)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/notes", map[string]any{"description": " from the phone ", "baseDescription": ""}), http.StatusOK)
	if versionOf(harness.rollOf(rollID)) != current || harness.auditCount(rollID) != audited {
		test.Fatal("a replayed note must not write")
	}

	// Ordering 2: the dashboard changes the notes first, then the phone's note on the old base.
	edited := harness.expect(harness.callWith("PUT", "/rolls/"+rollID, ifMatch(current), stockEdit("from the dashboard")), http.StatusOK)
	afterEdit := versionOf(asObject(edited.Body["roll"]))
	if afterEdit != current+1 {
		test.Fatalf("version = %d, want %d", afterEdit, current+1)
	}
	conflict := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/notes", map[string]any{"description": "phone again", "baseDescription": "from the phone"}), http.StatusConflict)
	if conflict.Body["code"] != "roll_notes_conflict" || harness.rollOf(rollID)["description"] != "from the dashboard" {
		test.Fatalf("conflict = %s", conflict.Raw)
	}

	// Malformed preconditions are refused, not ignored; an old client without the header still writes.
	for _, malformed := range []string{`3`, `W/"3"`, `"3`, `""`} {
		harness.expect(harness.callWith("PUT", "/rolls/"+rollID, map[string]string{"If-Match": malformed}, stockEdit("x")), http.StatusBadRequest)
	}
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/notes", map[string]any{"description": "x", "baseDescription": nil}), http.StatusBadRequest)
	if harness.rollOf(rollID)["description"] != "from the dashboard" {
		test.Fatal("a refused request must not write")
	}
	harness.expect(harness.call("PUT", "/rolls/"+rollID, stockEdit("legacy client")), http.StatusOK)

	// Finish and a lab job (a status change made by the processing service) move it too.
	beforeFinish := versionOf(harness.rollOf(rollID))
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/finish", map[string]any{"date": "2026-09-28"}), http.StatusOK)
	finished := versionOf(harness.rollOf(rollID))
	labID := newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab"}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), map[string]any{"labId": labID, "type": "develop"}), http.StatusCreated)
	atLab := harness.rollOf(rollID)
	if beforeFinish >= finished || finished >= versionOf(atLab) || atLab["status"] != "at_lab" {
		test.Fatalf("versions %d, %d, %d (status %v)", beforeFinish, finished, versionOf(atLab), atLab["status"])
	}
}

func TestFinishAndLoadReplaysOnTheRealDatabase(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	load := map[string]any{"cameraId": seeded.slrCamera, "startedAt": "2026-09-01", "shotIso": 800, "lensIds": []string{seeded.fiftyMmLensID}}

	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/load", load), http.StatusOK)
	audited := harness.auditCount(rollID)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/load", load), http.StatusOK)
	for field, value := range map[string]any{"startedAt": "2026-09-02", "shotIso": 1600, "lensIds": []string{}} {
		changed := map[string]any{}
		for key, original := range load {
			changed[key] = original
		}
		changed[field] = value
		result := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/load", changed), http.StatusConflict)
		if result.Body["code"] != "roll_loaded_differently" {
			test.Fatalf("%s: %s", field, result.Raw)
		}
	}
	if harness.auditCount(rollID) != audited {
		test.Fatal("load repeats must not write")
	}

	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/finish", map[string]any{"date": "2026-09-28"}), http.StatusOK)
	audited = harness.auditCount(rollID)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/finish", map[string]any{"date": "2026-09-28"}), http.StatusOK)
	if harness.auditCount(rollID) != audited {
		test.Fatal("a finish replay must not write")
	}
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/finish", map[string]any{"date": "2026-09-29"}), http.StatusConflict)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/load", load), http.StatusConflict) // the roll has moved on
}

func TestConcurrentConditionalRollWritesHaveOneWinner(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/load", map[string]any{"cameraId": seeded.slrCamera}), http.StatusOK)
	const writers = 8

	notes := race(writers, func(index int) response {
		return harness.call("PUT", "/rolls/"+rollID+"/notes", map[string]any{"description": fmt.Sprint("phone ", index), "baseDescription": ""})
	})
	if notes[http.StatusOK] != 1 || notes[http.StatusConflict] != writers-1 {
		test.Fatalf("roll notes statuses = %v", notes)
	}

	version := versionOf(harness.rollOf(rollID))
	edits := race(writers, func(index int) response {
		return harness.callWith("PUT", "/rolls/"+rollID, ifMatch(version), map[string]any{
			"filmStockId": seeded.stockID, "format": 135, "exposures": 36, "price": 1000 + index,
		})
	})
	if edits[http.StatusOK] != 1 || edits[http.StatusPreconditionFailed] != writers-1 {
		test.Fatalf("full edit statuses = %v", edits)
	}
	if versionOf(harness.rollOf(rollID)) != version+1 {
		test.Fatal("exactly one edit must have landed")
	}

	// A mobile note and a stale dashboard edit racing each other: the note is never lost.
	version = versionOf(harness.rollOf(rollID))
	base, _ := harness.rollOf(rollID)["description"].(string) // absent ("") after the edits above, which sent none
	mixed := race(2, func(index int) response {
		if index == 0 {
			return harness.call("PUT", "/rolls/"+rollID+"/notes", map[string]any{"description": "the note", "baseDescription": base})
		}
		return harness.callWith("PUT", "/rolls/"+rollID, ifMatch(version), map[string]any{
			"filmStockId": seeded.stockID, "format": 135, "exposures": 36, "description": "the form",
		})
	})
	description := harness.rollOf(rollID)["description"]
	switch {
	case mixed[http.StatusOK] == 1 && mixed[http.StatusPreconditionFailed] == 1 && description == "the note":
	case mixed[http.StatusOK] == 1 && mixed[http.StatusConflict] == 1 && description == "the form":
	default:
		test.Fatalf("statuses = %v, description = %v", mixed, description)
	}

	frames := race(writers, func(index int) response {
		return harness.call("PUT", "/rolls/"+rollID+"/frames/5", map[string]any{"notes": fmt.Sprint("frame ", index), "baseNotes": ""})
	})
	if frames[http.StatusOK] != 1 || frames[http.StatusConflict] != writers-1 {
		test.Fatalf("frame notes statuses = %v", frames)
	}
	if listed := harness.listOf("/rolls/" + rollID + "/frames"); len(listed) != 1 {
		test.Fatalf("frames = %v", listed)
	}
}

func TestCreateOnlyPutIsAtomicOwnerScopedAndNeverUpdates(test *testing.T) {
	harness := newHarness(test)
	createOnly := map[string]string{"If-None-Match": "*"}
	// createAudits is what one create writes to the audit log under the record's id: a fixed-lens
	// camera also links its built-in lens (camera_lens is audited by camera id).
	resources := []struct {
		path         string
		body         func(index int) map[string]any
		field        string
		createAudits int
	}{
		{"/labs/", func(index int) map[string]any { return map[string]any{"name": fmt.Sprint("Lab ", index)} }, "name", 1},
		{"/cameras/", func(index int) map[string]any {
			return map[string]any{"brand": "Canon", "model": fmt.Sprint("QL", index), "hasFixedLens": true, "fixedLens": map[string]any{"focalLength": 40, "maxAperture": 1.7}}
		}, "model", 2},
		{"/film-stocks/", func(index int) map[string]any {
			return map[string]any{"brand": "Kodak", "name": fmt.Sprint("Gold ", index), "type": "color", "boxIso": "200", "process": "C-41", "packaging": "factory"}
		}, "name", 1},
		{"/lenses/", func(index int) map[string]any {
			return map[string]any{"brand": "Nikon", "model": fmt.Sprint("50 ", index), "mount": "F", "focalLength": 50, "maxAperture": 1.4}
		}, "model", 1},
	}
	for _, resource := range resources {
		test.Run(resource.path, func(test *testing.T) {
			id := newID()
			const writers = 8
			statuses := race(writers, func(index int) response {
				return harness.callWith("PUT", resource.path+id, createOnly, resource.body(index))
			})
			if statuses[http.StatusCreated] != 1 || statuses[http.StatusPreconditionFailed] != writers-1 {
				test.Fatalf("statuses = %v", statuses)
			}
			stored := harness.expect(harness.call("GET", resource.path+id, nil), http.StatusOK).Body
			if resource.path == "/film-stocks/" {
				stored = asObject(stored["stock"])
			}
			audited := harness.auditCount(id)
			if audited != resource.createAudits {
				test.Fatalf("audit rows = %d, want %d (one create)", audited, resource.createAudits)
			}

			// A replay changes nothing and writes no audit row.
			replay := harness.expect(harness.callWith("PUT", resource.path+id, createOnly, resource.body(99)), http.StatusPreconditionFailed)
			if replay.Body["code"] != "already_exists" || harness.auditCount(id) != audited {
				test.Fatalf("replay = %s", replay.Raw)
			}
			again := harness.expect(harness.call("GET", resource.path+id, nil), http.StatusOK).Body
			if resource.path == "/film-stocks/" {
				again = asObject(again["stock"])
			}
			if again[resource.field] != stored[resource.field] {
				test.Fatalf("a create-only PUT updated the record: %v -> %v", stored[resource.field], again[resource.field])
			}

			// Another account cannot create over the id, learns nothing about the record, and changes nothing.
			foreign := harness.expect(harness.callAs("bob", "PUT", resource.path+id, resource.body(7)), http.StatusConflict)
			bobRequest := httptest.NewRequest("PUT", resource.path+id, bytes.NewReader(mustJSON(resource.body(7))))
			bobRequest.Header.Set("Content-Type", "application/json")
			bobRequest.Header.Set("If-None-Match", "*")
			bobRequest.AddCookie(harness.sessionOf("bob"))
			taken := harness.expect(harness.serve(bobRequest), http.StatusPreconditionFailed)
			if len(taken.Body) != 2 || taken.Body["code"] != "already_exists" || len(foreign.Body) != 2 {
				test.Fatalf("a foreign id must answer only code and message: %s / %s", taken.Raw, foreign.Raw)
			}
			harness.expect(harness.callAs("bob", "GET", resource.path+id, nil), http.StatusNotFound)
			if harness.auditCount(id) != audited {
				test.Fatal("refused creates must not write audit rows")
			}

			// Malformed If-None-Match is refused.
			harness.expect(harness.callWith("PUT", resource.path+newID(), map[string]string{"If-None-Match": `"abc"`}, resource.body(1)), http.StatusBadRequest)
		})
	}

	// A deleted id stays reserved.
	labID := newID()
	harness.expect(harness.callWith("PUT", "/labs/"+labID, createOnly, map[string]any{"name": "Gone"}), http.StatusCreated)
	harness.expect(harness.call("DELETE", "/labs/"+labID, nil), http.StatusNoContent)
	harness.expect(harness.callWith("PUT", "/labs/"+labID, createOnly, map[string]any{"name": "Back"}), http.StatusPreconditionFailed)
}

func mustJSON(value any) []byte {
	payload, _ := json.Marshal(value)
	return payload
}
