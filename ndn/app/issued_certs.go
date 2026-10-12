package app

import (
	"sort"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/security"
	"github.com/pulsejet/ownly/ndn/app/tlv"
)

type issuedWorkspaceCertIndex map[string]map[string]bool

func (idx issuedWorkspaceCertIndex) remember(certName, group enc.Name) bool {
	if !tlv.IsWkspKeyCertName(certName) || len(group) == 0 {
		return false
	}
	cert := certName.String()
	groupName := group.String()
	groups, ok := idx[cert]
	if !ok {
		groups = make(map[string]bool)
		idx[cert] = groups
	}
	if groups[groupName] {
		return false
	}
	groups[groupName] = true
	return true
}

func (idx issuedWorkspaceCertIndex) forMember(group, workspace, member enc.Name) []enc.Name {
	target := workspace.Append(member...)
	if workspace.IsPrefix(member) {
		target = member
	}
	groupName := group.String()
	out := make([]enc.Name, 0)
	for cert, groups := range idx {
		if !groups[groupName] {
			continue
		}
		certName, err := enc.NameFromStr(cert)
		if err != nil || !tlv.IsWkspKeyCertName(certName) {
			continue
		}
		identity, err := security.GetIdentityFromCertName(certName)
		if err != nil || !identity.Equal(target) {
			continue
		}
		out = append(out, certName)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].String() < out[j].String()
	})
	return out
}
