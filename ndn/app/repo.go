//go:build js && wasm

package app

import (
	"time"

	spec_repo "github.com/named-data/ndnd/repo/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
)

func (a *App) NotifyRepoJoin(client ndn.Client, group enc.Name, dataPrefix enc.Name, snapshot bool) {
	// Wait for 1s so that routes get registered
	time.Sleep(time.Second)
	syncJoin := &spec_repo.SyncJoin{
		Protocol:        &spec.NameContainer{Name: spec_repo.SyncProtocolSvsV3},
		Group:           &spec.NameContainer{Name: group},
		MulticastPrefix: &spec.NameContainer{Name: multicastPrefix},
	}
	if snapshot {
		syncJoin.HistorySnapshot = &spec_repo.HistorySnapshotConfig{
			Threshold: SnapshotThreshold,
		}
	}
	idSigner, _ := a.GetTestbedKey()
	repoCmd := spec_repo.RepoCmd{SyncJoin: syncJoin}
	client.ExpressCommand(
		repoName,
		idSigner.KeyName().Append(enc.NewKeywordComponent("repo-cmd")),
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
func (a *App) NotifyRepoLeave(client ndn.Client, group enc.Name, dataPrefix enc.Name) {
	repoCmd := spec_repo.RepoCmd{
		SyncLeave: &spec_repo.SyncLeave{
			Protocol: &spec.NameContainer{Name: spec_repo.SyncProtocolSvsV3},
			Group:    &spec.NameContainer{Name: group},
		},
	}
	idSigner, _ := a.GetTestbedKey()
	client.ExpressCommand(
		repoName,
		idSigner.KeyName().Append(enc.NewKeywordComponent("repo-cmd")),
		repoCmd.Encode(),
		func(w enc.Wire, err error) {
			if err != nil {
				log.Warn(nil, "Repo sync leave command failed", "group", group, "err", err)
			} else {
				log.Info(nil, "Repo left SVS group", "group", group)
			}
		})
}
