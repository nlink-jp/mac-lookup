package ouidb

import "testing"

func TestRegistryBits(t *testing.T) {
	tests := []struct {
		registry Registry
		want     int
	}{
		{RegistryMAL, 24},
		{RegistryMAM, 28},
		{RegistryMAS, 36},
		{RegistryIAB, 36},
		{RegistryCID, 24},
	}
	for _, tt := range tests {
		if got := tt.registry.Bits(); got != tt.want {
			t.Errorf("Registry(%q).Bits() = %d, want %d", tt.registry, got, tt.want)
		}
	}
}

func TestEntryPrivate(t *testing.T) {
	// IEEE lets a registrant withhold their name. "Private" means assigned but
	// undisclosed, which callers must not flatten into "unassigned".
	if !(Entry{Organization: PrivateOrganization}).Private() {
		t.Error(`Entry{Organization: "Private"}.Private() = false, want true`)
	}
	if (Entry{Organization: "Nokia Shanghai Bell Co., Ltd."}).Private() {
		t.Error("named registrant reported as Private")
	}
	// An empty organization is an unassigned or malformed row, not a Private
	// registration.
	if (Entry{}).Private() {
		t.Error("empty organization reported as Private")
	}
}
