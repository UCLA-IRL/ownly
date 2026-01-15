//go:build js && wasm

package app

import (
	"fmt"
	"time"

	spec_repo "github.com/named-data/ndnd/repo/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/object"
)

func (a *App) publishSecurityConfig(client ndn.Client, group enc.Name) (*spec.NameContainer, error) {
	anchors := a.GatherAnchors()
	if len(anchors) == 0 {
		return nil, fmt.Errorf("no identity or peer anchors available")
	}

	cfg := &spec_repo.SecurityConfigObject{
		Schema:  append([]byte(nil), SchemaBytes...),
		Anchors: anchors,
	}

	testbedSigner, _ := a.GetTestbedKey()
	name := testbedSigner.KeyName().
		Append(enc.NewKeywordComponent("repo-cmd")).
		Append(enc.NewKeywordComponent("syncjoin")).
		Append(group...).
		Append(enc.NewKeywordComponent("sec-cfg")).
		Append(enc.NewVersionComponent(uint64(time.Now().UnixMicro())))

	_, err := object.Produce(ndn.ProduceArgs{
		Name:            name,
		Content:         cfg.Encode(),
		FreshnessPeriod: time.Hour,
		NoMetadata:      true,
	}, client.Store(), testbedSigner)
	if err != nil {
		return nil, err
	}

	return &spec.NameContainer{Name: name}, nil
}

func (a *App) NotifyRepoJoin(client ndn.Client, group enc.Name, dataPrefix enc.Name, snapshot bool) {
	// Wait for 1s so that routes get registered
	time.Sleep(time.Second)

	secCfg, err := a.publishSecurityConfig(client, group)
	if err != nil {
		log.Warn(a, "Failed to publish security config", "group", group, "err", err)
	}

	syncJoin := &spec_repo.SyncJoin{
		Protocol:        &spec.NameContainer{Name: spec_repo.SyncProtocolSvsV3},
		Group:           &spec.NameContainer{Name: group},
		MulticastPrefix: &spec.NameContainer{Name: multicastPrefix},
		SecurityConfig:  secCfg,
	}
	if snapshot {
		syncJoin.HistorySnapshot = &spec_repo.HistorySnapshotConfig{
			Threshold: SnapshotThreshold,
		}
	}

	testbedSigner, _ := a.GetTestbedKey()
	repoCmd := spec_repo.RepoCmd{SyncJoin: syncJoin}
	name := testbedSigner.KeyName().
		Append(enc.NewKeywordComponent("repo-cmd")).
		Append(enc.NewKeywordComponent("syncjoin")).
		Append(group...).
		Append(enc.NewKeywordComponent("sync-cfg")).
		Append(enc.NewVersionComponent(uint64(time.Now().UnixMicro())))

	client.ExpressCommand(
		repoName,
		name,
		repoCmd.Encode(),
		func(w enc.Wire, err error) {
			if err != nil {
				log.Warn(nil, "Repo sync join command failed", "group", group, "err", err)
			} else {
				log.Info(nil, "Repo joined SVS group", "group", group)
			}
		})
}

// Unused
func (a *App) NotifyRepoLeave(client ndn.Client, group enc.Name) {
	repoCmd := spec_repo.RepoCmd{
		SyncLeave: &spec_repo.SyncLeave{
			Protocol: &spec.NameContainer{Name: spec_repo.SyncProtocolSvsV3},
			Group:    &spec.NameContainer{Name: group},
		},
	}
	testbedSigner, _ := a.GetTestbedKey()
	name := testbedSigner.KeyName().
		Append(enc.NewKeywordComponent("repo-cmd")).
		Append(enc.NewKeywordComponent("syncleave")).
		Append(group...).
		Append(enc.NewKeywordComponent("sync-cfg")).
		Append(enc.NewVersionComponent(uint64(time.Now().UnixMicro())))
	client.ExpressCommand(
		repoName,
		name,
		repoCmd.Encode(),
		func(w enc.Wire, err error) {
			if err != nil {
				log.Warn(nil, "Repo sync leave command failed", "group", group, "err", err)
			} else {
				log.Info(nil, "Repo left SVS group", "group", group)
			}
		})
}
