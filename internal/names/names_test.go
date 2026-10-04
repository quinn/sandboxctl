package names

import "testing"

func TestNetworkIdentityIsStable(t *testing.T) {
	tap1, mac1, host1 := NetworkIdentity("dev1")
	tap2, mac2, host2 := NetworkIdentity("dev1")
	if tap1 != tap2 || mac1 != mac2 || host1 != host2 {
		t.Fatal("the network identity is not stable")
	}
	if len(tap1) > 15 {
		t.Fatalf("the TAP name is too long: %q", tap1)
	}
}
