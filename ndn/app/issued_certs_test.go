package app

import (
	"encoding/json"
	"testing"

	enc "github.com/named-data/ndnd/std/encoding"
)

func issuedCertTestName(t *testing.T, value string) enc.Name {
	t.Helper()
	name, err := enc.NameFromStr(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return name
}

func TestIssuedWorkspaceCertIndexForMember(t *testing.T) {
	group := issuedCertTestName(t, "/alice/wksp/32=boot")
	otherGroup := issuedCertTestName(t, "/carol/wksp/32=boot")
	index := make(issuedWorkspaceCertIndex)

	aliceV1 := issuedCertTestName(t, "/alice/wksp/bob/KEY/k1/anchor/v=1")
	aliceV2 := issuedCertTestName(t, "/alice/wksp/bob/KEY/k2/anchor/v=2")
	otherMember := issuedCertTestName(t, "/alice/wksp/carol/KEY/k1/anchor/v=1")
	otherWorkspace := issuedCertTestName(t, "/carol/wksp/bob/KEY/k1/anchor/v=1")
	identityCert := issuedCertTestName(t, "/bob/KEY/k1/identity/v=1")

	for _, cert := range []enc.Name{aliceV2, aliceV1, otherMember} {
		if !index.remember(cert, group) {
			t.Fatalf("remember(%s) = false", cert)
		}
	}
	if !index.remember(otherWorkspace, otherGroup) {
		t.Fatalf("remember(%s) = false", otherWorkspace)
	}
	if index.remember(identityCert, group) {
		t.Fatal("identity certificate was added to workspace certificate index")
	}
	if index.remember(aliceV1, group) {
		t.Fatal("duplicate certificate changed the index")
	}

	workspace := issuedCertTestName(t, "/alice/wksp")
	member := issuedCertTestName(t, "/bob")
	got := index.forMember(group, workspace, member)
	if len(got) != 2 || !got[0].Equal(aliceV1) || !got[1].Equal(aliceV2) {
		t.Fatalf("forMember() = %v, want [%s %s]", got, aliceV1, aliceV2)
	}

	qualifiedMember := issuedCertTestName(t, "/alice/wksp/bob")
	got = index.forMember(group, workspace, qualifiedMember)
	if len(got) != 2 {
		t.Fatalf("forMember(qualified) returned %d certificates, want 2", len(got))
	}

	wire, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	reloaded := make(issuedWorkspaceCertIndex)
	if err := json.Unmarshal(wire, &reloaded); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	got = reloaded.forMember(group, workspace, member)
	if len(got) != 2 || !got[0].Equal(aliceV1) || !got[1].Equal(aliceV2) {
		t.Fatalf("reloaded forMember() = %v, want [%s %s]", got, aliceV1, aliceV2)
	}
}
