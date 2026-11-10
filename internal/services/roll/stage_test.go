package roll

import (
	"testing"

	"meta-frames-server/internal/domain"
)

func TestRollStage(test *testing.T) {
	developedOnly, scannedTarget := targetDeveloped, targetScanned
	self, lab := actorSelf, actorLab
	homeDevelopDone := stageJob{Type: domain.JobTypeDevelop, Developed: true, NegativesBack: true}
	labDevelopDone := stageJob{Type: domain.JobTypeDevelop, AtLab: true, Developed: true} // negatives still at the lab
	labDevelopScanDone := stageJob{Type: domain.JobTypeDevelopScan, AtLab: true, Developed: true, ScansBack: true}

	cases := []struct {
		name       string
		status     string
		target     *string
		scanBy     *string
		jobs       []stageJob
		wantStage  string
		wantDetail string
	}{
		{"before processing keeps the raw status", domain.RollStatusDoneShooting, &scannedTarget, &lab, nil, domain.RollStatusDoneShooting, ""},

		// Every develop-by / scan-by / target combination ends completed.
		{"self develop, develop only", domain.RollStatusDeveloped, &developedOnly, nil, []stageJob{homeDevelopDone}, stageCompleted, ""},
		{"lab develop, develop only, negatives still at lab", domain.RollStatusAtLab, &developedOnly, nil, []stageJob{labDevelopDone}, stageCompleted, ""},
		{"lab develop + scan, negatives still at lab", domain.RollStatusScanned, &scannedTarget, &lab, []stageJob{labDevelopScanDone}, stageCompleted, ""},
		{"self develop, self scan", domain.RollStatusScanned, &scannedTarget, &self, []stageJob{homeDevelopDone, {Type: domain.JobTypeScan, ScansBack: true}}, stageCompleted, ""},
		{"lab develop, self scan", domain.RollStatusScanned, &scannedTarget, &self, []stageJob{{Type: domain.JobTypeDevelop, AtLab: true, Developed: true, NegativesBack: true}, {Type: domain.JobTypeScan, ScansBack: true}}, stageCompleted, ""},
		{"self develop, lab scan", domain.RollStatusScanned, &scannedTarget, &lab, []stageJob{homeDevelopDone, {Type: domain.JobTypeScan, AtLab: true, ScansBack: true}}, stageCompleted, ""},

		// Unfinished jobs: the badge follows the job, not the plan.
		{"lab developing", domain.RollStatusAtLab, &scannedTarget, &lab, []stageJob{{Type: domain.JobTypeDevelopScan, AtLab: true}}, stageProcessing, detailLabDeveloping},
		{"lab returned negatives before scans", domain.RollStatusAtLab, &scannedTarget, &lab, []stageJob{{Type: domain.JobTypeDevelopScan, AtLab: true, NegativesBack: true}}, stageProcessing, detailLabScanning},
		{"lab developed, now scanning", domain.RollStatusAtLab, &scannedTarget, &lab, []stageJob{{Type: domain.JobTypeDevelopScan, AtLab: true, Developed: true}}, stageProcessing, detailLabScanning},
		{"home developing", domain.RollStatusAtLab, nil, nil, []stageJob{{Type: domain.JobTypeDevelop}}, stageProcessing, detailHomeDeveloping},
		{"plan says self, job is at a lab", domain.RollStatusAtLab, nil, nil, []stageJob{{Type: domain.JobTypeDevelop, AtLab: true}}, stageProcessing, detailLabDeveloping},
		{"first scan at home", domain.RollStatusAtLab, &scannedTarget, &self, []stageJob{homeDevelopDone, {Type: domain.JobTypeScan}}, stageProcessing, detailHomeScanning},
		{"rescan after earlier scans", domain.RollStatusAtLab, &scannedTarget, &lab, []stageJob{labDevelopScanDone, {Type: domain.JobTypeScan, AtLab: true}}, stageProcessing, detailRescanning},
		{"open print job", domain.RollStatusAtLab, nil, nil, []stageJob{labDevelopScanDone, {Type: domain.JobTypePrint, AtLab: true}}, stageProcessing, detailPrinting},
		{"earliest step wins the badge", domain.RollStatusAtLab, nil, nil, []stageJob{{Type: domain.JobTypePrint, AtLab: true}, {Type: domain.JobTypeDevelop, AtLab: true}}, stageProcessing, detailLabDeveloping},

		// Developed, target scanned, no scan yet: waiting, by who scans.
		{"waiting, scanner not chosen", domain.RollStatusDeveloped, &scannedTarget, nil, []stageJob{homeDevelopDone}, stageProcessing, detailWaitingScan},
		{"waiting for a home scan", domain.RollStatusDeveloped, &scannedTarget, &self, []stageJob{homeDevelopDone}, stageProcessing, detailWaitingHomeScan},
		{"waiting for a lab scan", domain.RollStatusAtLab, &scannedTarget, &lab, []stageJob{labDevelopDone}, stageProcessing, detailWaitingLabScan},

		// Old data.
		{"no plan counts as completed", domain.RollStatusDeveloped, nil, nil, []stageJob{homeDevelopDone}, stageCompleted, ""},
		{"closed print-only roll", domain.RollStatusDeveloped, nil, nil, []stageJob{{Type: domain.JobTypePrint, AtLab: true, NegativesBack: true}}, stageCompleted, ""},
		{"print-only roll with a scan target is not waiting", domain.RollStatusDeveloped, &scannedTarget, nil, []stageJob{{Type: domain.JobTypePrint, NegativesBack: true}}, stageCompleted, ""},
	}
	for _, testCase := range cases {
		stage, detail := rollStage(testCase.status, testCase.target, testCase.scanBy, testCase.jobs)
		gotDetail := ""
		if detail != nil {
			gotDetail = *detail
		}
		if stage != testCase.wantStage || gotDetail != testCase.wantDetail {
			test.Errorf("%s: got (%s, %q), want (%s, %q)", testCase.name, stage, gotDetail, testCase.wantStage, testCase.wantDetail)
		}
	}
}
