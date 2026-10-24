package main

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func full() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://db/np", "KAFKA_BROKERS": " b1:9092 , ,b2:9092",
		envVendorGroup: "np-vendor", envShippingGroup: "np-shipping", envPromiseGroup: "np-promise",
		envOfferGroup: "np-offer", envTopologyGroup: "np-topology", envSellerGroup: "np-seller",
	}
}

func quiet() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	return slog.New(slog.NewTextHandler(&b, nil)), &b
}

func TestLoadConfigDefaults(t *testing.T) {
	logger, _ := quiet()
	cfg, err := loadConfig(envOf(full()), logger)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.databaseURL != "postgres://db/np" || cfg.migrationsURL != "postgres://db/np" || cfg.brokerList() != "b1:9092,b2:9092" || len(cfg.brokers) != 2 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.addr != ":8080" || cfg.recomputeInterval != time.Minute || cfg.relayInterval != time.Second ||
		cfg.maxAttempts != 20 || cfg.drainDelay != 5*time.Second {
		t.Fatalf("defaults = %+v", cfg)
	}
	assertGroups(t, cfg)
}

func TestLoadConfigReadsTheOverrides(t *testing.T) {
	logger, _ := quiet()
	env := full()
	env["MIGRATIONS_DATABASE_URL"] = "postgres://direct/np"
	env[envHTTPAddr], env[envRecomputeInterval], env[envRelayInterval] = ":9000", "30s", "250ms"
	env[envMaxAttempts], env[envShutdownDrainWait], env[envLogLevel] = "0", "0", "debug"
	cfg, err := loadConfig(envOf(env), logger)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.migrationsURL != "postgres://direct/np" || cfg.addr != ":9000" || cfg.recomputeInterval != 30*time.Second ||
		cfg.relayInterval != 250*time.Millisecond || cfg.maxAttempts != 0 || cfg.drainDelay != 0 || newLogLevel(cfg.logLevel) != slog.LevelDebug {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestEveryMandatoryVariableIsNamedInItsBootError(t *testing.T) {
	logger, _ := quiet()
	for _, name := range []string{"DATABASE_URL", envVendorGroup, envShippingGroup, envPromiseGroup, envOfferGroup, envTopologyGroup, envSellerGroup} {
		env := full()
		delete(env, name)
		_, err := loadConfig(envOf(env), logger)
		if !errors.Is(err, postgres.ErrMissingEnv) || !strings.Contains(err.Error(), name) {
			t.Errorf("without %s: %v", name, err)
		}
	}
	for _, blank := range []string{"", " , ,"} {
		env := full()
		env["KAFKA_BROKERS"] = blank
		if _, err := loadConfig(envOf(env), logger); !errors.Is(err, ErrNoBrokers) {
			t.Errorf("brokers %q: %v", blank, err)
		}
	}
}

func TestBadOptionalValuesFallBackAndWarn(t *testing.T) {
	logger, logs := quiet()
	env := full()
	env[envRecomputeInterval], env[envRelayInterval], env[envMaxAttempts], env[envShutdownDrainWait] = "soon", "-3", "many", "-1s"
	cfg, err := loadConfig(envOf(env), logger)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.recomputeInterval != time.Minute || cfg.relayInterval != time.Second || cfg.maxAttempts != 20 || cfg.drainDelay != 5*time.Second {
		t.Fatalf("cfg = %+v", cfg)
	}
	for _, name := range []string{envRecomputeInterval, envRelayInterval, envMaxAttempts, envShutdownDrainWait} {
		if !strings.Contains(logs.String(), name) {
			t.Errorf("no warning names %s:\n%s", name, logs.String())
		}
	}
	if _, err := loadConfig(envOf(map[string]string{}), logger); err == nil {
		t.Fatal("an empty environment must not boot")
	}
}

func TestIntervalsAcceptPlainSecondsAndNegativeAttemptsFallBack(t *testing.T) {
	logger, _ := quiet()
	if d := parseInterval("X", "2.5", time.Hour, logger); d != 2500*time.Millisecond {
		t.Fatalf("2.5 seconds = %v", d)
	}
	if n := outboxMaxAttempts("-2", logger); n != 20 {
		t.Fatalf("negative attempts = %d", n)
	}
	if n := outboxMaxAttempts(" 7 ", logger); n != 7 {
		t.Fatalf("attempts = %d", n)
	}
}

func TestLogLevels(t *testing.T) {
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "Error": slog.LevelError, "": slog.LevelInfo, "loud": slog.LevelInfo} {
		if got := newLogLevel(in); got != want {
			t.Errorf("%q = %v, want %v", in, got, want)
		}
	}
}

func assertGroups(t *testing.T, cfg config) {
	t.Helper()
	if g := cfg.groups; g.VendorConnectivity != "np-vendor" || g.ShippingAndTracking != "np-shipping" || g.DeliveryPromise != "np-promise" ||
		g.OfferAvailability != "np-offer" || g.NodeTopology != "np-topology" || g.SellerInbound != "np-seller" {
		t.Fatalf("groups = %+v", cfg.groups)
	}
}
