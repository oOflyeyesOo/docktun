package handler

import (
	"math"
	"testing"
)

func TestParseDiskOutput(t *testing.T) {
	// Case 1: Standard POSIX or unwrapped single-line df output
	standardOutput := `Filesystem     1024-blocks      Used Available Capacity Mounted on
/dev/sda1        102604620  15518720  81861780      16% /app/data`

	total, used, free, pct := parseDiskOutput(standardOutput)
	if total != 102604620*1024 {
		t.Fatalf("expected total %d, got %d", 102604620*1024, total)
	}
	if used != 15518720*1024 {
		t.Fatalf("expected used %d, got %d", 15518720*1024, used)
	}
	if free != 81861780*1024 {
		t.Fatalf("expected free %d, got %d", 81861780*1024, free)
	}
	if math.Abs(pct-15.12) > 0.1 {
		t.Fatalf("expected pct ~15.12, got %f", pct)
	}

	// Case 2: GNU df with long device name wrapped onto next line (Claude's exact reported bug!)
	wrappedOutput := `Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/mapper/syno_vg1-volume_data
                     102604620  15518720  81861780  16% /app/data`

	totalW, usedW, freeW, pctW := parseDiskOutput(wrappedOutput)
	if totalW != 102604620*1024 {
		t.Fatalf("expected total %d, got %d", 102604620*1024, totalW)
	}
	if usedW != 15518720*1024 {
		t.Fatalf("expected used %d, got %d", 15518720*1024, usedW)
	}
	if freeW != 81861780*1024 {
		t.Fatalf("expected free %d, got %d", 81861780*1024, freeW)
	}
	if math.Abs(pctW-15.12) > 0.1 {
		t.Fatalf("expected pct ~15.12, got %f", pctW)
	}

	// Case 3: Empty / invalid
	tE, uE, fE, pE := parseDiskOutput("")
	if tE != 0 || uE != 0 || fE != 0 || pE != 0 {
		t.Fatalf("expected zeroes for empty input, got %d, %d, %d, %f", tE, uE, fE, pE)
	}
}
