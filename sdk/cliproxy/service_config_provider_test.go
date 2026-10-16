package cliproxy

import (
	"fmt"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"gopkg.in/yaml.v3"
)

func TestProviderStrategiesReloadPreservesDefaultWRR(t *testing.T) {
	ctx := coreauth.WithSkipPersist(t.Context())
	const provider, model = "provider-reload-default", "provider-reload-model"
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{coreManager: manager}
	var picked string
	manager.RegisterExecutor(funcIssue7431Executor{provider: provider, onExec: func(a *coreauth.Auth) { picked = a.ID }})
	t.Cleanup(func() { manager.SetSelector(nil) })

	reg := registry.GetGlobalRegistry()
	for i := range 4 {
		id := fmt.Sprintf("provider-reload-auth-%d", i)
		reg.RegisterClient(id, provider, []*registry.ModelInfo{{ID: model}})
		t.Cleanup(func() { reg.UnregisterClient(id) })
		if _, err := manager.Register(ctx, &coreauth.Auth{ID: id, Provider: provider, Status: coreauth.StatusActive}); err != nil {
			t.Fatal(err)
		}
		manager.RefreshSchedulerEntry(id)
	}

	for i, extra := range []string{
		"",
		"  provider-strategies: {other-provider: least-used}\n",
		"  provider-strategies: {other-provider: p2c}\n  session-affinity: true\n",
		"",
	} {
		var cfg config.Config
		if err := yaml.Unmarshal([]byte("routing:\n  strategy: wrr\n"+extra), &cfg); err != nil {
			t.Fatal(err)
		}
		if !service.applyManagerConfig(ctx, configCommit{cfg: &cfg, sequence: uint64(i + 1)}) {
			t.Fatal("applyManagerConfig failed")
		}
		if _, wrapped := manager.Selector().(*coreauth.ProviderStrategySelector); (i == 1) != wrapped {
			t.Fatalf("reload %d: unexpected selector %T", i, manager.Selector())
		}
		if _, err := manager.Execute(ctx, []string{provider}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("provider-reload-auth-%d", i); picked != want {
			t.Fatalf("reload %d picked %q, want %q", i, picked, want)
		}
	}
}

func TestProviderStrategiesFromConfig(t *testing.T) {
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(`routing:
  strategy: ff
  provider-strategies:
    random-provider: random
    " LEAST-PROVIDER ": leastused
    p2c-provider: p2c
    default-provider: fillfirst
    unknown-provider: unsupported
`), &cfg); err != nil {
		t.Fatal(err)
	}
	selector := newRoutingSelector(normalizedRoutingRuntimeState(&cfg))
	wrapper, ok := selector.(*coreauth.ProviderStrategySelector)
	if !ok {
		t.Fatalf("selector = %T, want a provider override wrapper", selector)
	}
	if _, ok := wrapper.Default.(*coreauth.FillFirstSelector); !ok {
		t.Errorf("default selector = %T, want fill-first", wrapper.Default)
	}
	if _, ok := wrapper.ByProvider["random-provider"].(*coreauth.RandomSelector); !ok {
		t.Errorf("random-provider selector = %T, want random", wrapper.ByProvider["random-provider"])
	}
	if _, ok := wrapper.ByProvider["least-provider"].(*coreauth.LeastUsedSelector); !ok {
		t.Errorf("least-provider selector = %T, want least-used", wrapper.ByProvider["least-provider"])
	}
	if _, ok := wrapper.ByProvider["p2c-provider"].(*coreauth.P2CSelector); !ok {
		t.Errorf("p2c-provider selector = %T, want p2c", wrapper.ByProvider["p2c-provider"])
	}
	if len(wrapper.ByProvider) != 3 {
		t.Errorf("got %d overrides, want only the three non-default, recognized strategies", len(wrapper.ByProvider))
	}
}
