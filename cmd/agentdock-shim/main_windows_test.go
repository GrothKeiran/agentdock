//go:build windows

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/fs/processlock"
	"github.com/uvwt/agentdock/internal/installer"
	"github.com/uvwt/agentdock/internal/updateengine"
)

func TestInstallerTrialRequiresMatchingLiveOwner(t *testing.T) {
	for _, scenario := range []string{"live", "missing-lock", "released-lock", "wrong-id", "wrong-version", "wrong-root", "wrong-platform", "failed", "uninstall", "deferred-commit", "stale-update-journal"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			store, err := updateengine.NewStore(root)
			if err != nil {
				t.Fatal(err)
			}
			layout, err := updateengine.NewWindowsLayout(root)
			if err != nil {
				t.Fatal(err)
			}
			active := updateengine.ActiveVersion{SchemaVersion: 1, ActiveVersion: "v0.9.0", State: updateengine.StateTrial, TransactionID: "installer-test", UpdatedAt: time.Now().UTC()}
			if err := store.WriteActive(active); err != nil {
				t.Fatal(err)
			}
			installStore, err := installer.NewStore(root)
			if err != nil {
				t.Fatal(err)
			}
			tx := installer.Transaction{SchemaVersion: 1, TransactionID: active.TransactionID, Platform: "windows", Action: installer.ActionInstall, State: updateengine.StateTrial, Phase: installer.PhaseStart, TargetVersion: active.ActiveVersion, InstallRoot: root, RuntimeRoot: root}
			switch scenario {
			case "wrong-id":
				tx.TransactionID = "other"
			case "wrong-version":
				tx.TargetVersion = "v0.8.3"
			case "wrong-root":
				tx.InstallRoot = filepath.Join(root, "other")
			case "wrong-platform":
				tx.Platform = "linux"
			case "failed":
				tx.State = updateengine.StateFailed
			case "uninstall":
				tx.Action = installer.ActionUninstall
			case "deferred-commit":
				tx.Phase = installer.PhaseCommit
			}
			if err := installStore.WriteTransaction(tx); err != nil {
				t.Fatal(err)
			}
			if scenario != "missing-lock" {
				lock, err := processlock.Acquire(context.Background(), installStore.LockPath())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lock.Release() })
				if scenario == "released-lock" {
					if err := lock.Release(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scenario == "stale-update-journal" {
				old, err := updateengine.NewTransaction("windows", "v0.8.2", "v0.8.3")
				if err != nil {
					t.Fatal(err)
				}
				old.State = updateengine.StateCommitted
				old.Windows = &updateengine.WindowsPlan{InstallRoot: root}
				if err := store.WriteTransaction(old); err != nil {
					t.Fatal(err)
				}
			}
			got, err := resolveActiveWithRecovery(root, store, layout)
			if scenario == "live" || scenario == "stale-update-journal" {
				if err != nil || got.ActiveVersion != active.ActiveVersion {
					t.Fatalf("live trial: got %+v, %v", got, err)
				}
			} else if err == nil {
				t.Fatal("unsafe installer trial was allowed")
			}
		})
	}
}

func TestTrayRequiresWaitOnlyDetachesNormalBackgroundLaunches(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no arguments", want: false},
		{name: "background", args: []string{"--background"}, want: false},
		{name: "background case insensitive", args: []string{" --BACKGROUND "}, want: false},
		{name: "task admin", args: []string{"--task-admin", "prepare-elevated"}, want: true},
		{name: "version", args: []string{"--version"}, want: true},
		{name: "help", args: []string{"--help"}, want: true},
		{name: "background plus management argument", args: []string{"--background", "--start-core"}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := trayRequiresWait(test.args); got != test.want {
				t.Fatalf("trayRequiresWait(%q) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestCoreLaunchRequiresParentLifetimeOnlyForServiceHost(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "service host", args: []string{"service", "launch-core", "--runtime-root", `C:\AgentDock`}, want: true},
		{name: "service host case insensitive", args: []string{" SERVICE ", " LAUNCH-CORE "}, want: true},
		{name: "service status", args: []string{"service", "status"}},
		{name: "version", args: []string{"version", "--json"}},
		{name: "empty"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := coreLaunchRequiresParentLifetime(test.args); got != test.want {
				t.Fatalf("coreLaunchRequiresParentLifetime(%q) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}
