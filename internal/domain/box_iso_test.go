package domain

import "testing"

func TestValidBoxISO(test *testing.T) {
	for _, valid := range []string{"400", "100-400", "50-200", "999999"} {
		if !ValidBoxISO(valid) {
			test.Errorf("%q should be valid", valid)
		}
	}
	for _, invalid := range []string{"", "0", "-400", "400-", "400-100", "200-200", "100-200-400", "1000000", "ISO 400", " 400", "100 - 400"} {
		if ValidBoxISO(invalid) {
			test.Errorf("%q should be invalid", invalid)
		}
	}
}

func TestSingleBoxISO(test *testing.T) {
	if iso, single := SingleBoxISO("400"); !single || iso != 400 {
		test.Fatalf("iso=%d single=%v", iso, single)
	}
	if _, single := SingleBoxISO("100-400"); single {
		test.Fatal("a range has no single speed")
	}
}
