package cmd

import (
	"testing"

	"RealityChecker/internal/storage"
)

func TestParseDomains(t *testing.T) {
	input := "apple.com google.com apple.com invalid..domain test.org"
	valid, invalid, duplicates := parseDomains(input)

	if len(valid) != 3 {
		t.Fatalf("expected 3 valid domains, got %d (%v)", len(valid), valid)
	}
	if len(duplicates) != 1 || duplicates[0] != "apple.com" {
		t.Fatalf("expected 1 duplicate (apple.com), got %v", duplicates)
	}
	if len(invalid) != 1 || invalid[0] != "invalid..domain" {
		t.Fatalf("expected 1 invalid (invalid..domain), got %v", invalid)
	}
}

func TestCalculateRemainingIPs(t *testing.T) {
	cidrs := []string{
		"192.168.1.0/24",
		"203.0.113.0/24",
	}

	// 1. No checkpoints: 256 + 256 = 512
	total := CalculateRemainingIPs(cidrs, nil)
	if total != 512 {
		t.Errorf("expected 512, got %d", total)
	}

	// 2. 192.168.1.0/24 completed: 0 + 256 = 256
	checkpoints := map[string]*storage.CheckpointRecord{
		"192.168.1.0/24": {
			CIDR:      "192.168.1.0/24",
			Completed: true,
		},
		"203.0.113.0/24": {
			CIDR:      "203.0.113.0/24",
			LastIP:    "203.0.113.0", // 1 IP scanned, 255 remaining
			Completed: false,
		},
	}
	remaining := CalculateRemainingIPs(cidrs, checkpoints)
	if remaining != 255 {
		t.Errorf("expected 255, got %d", remaining)
	}
}

