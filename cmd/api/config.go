package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	inboundkafka "github.com/claudioed/node-performance/internal/adapters/inbound/kafka"
	outboxrelay "github.com/claudioed/node-performance/internal/adapters/outbound/outbox"
	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
)

// The environment variables of the service. DATABASE_URL, KAFKA_BROKERS and the
// five consumer groups are mandatory: a missing one is a boot error naming it,
// raised before anything is opened or bound.
const (
	envKafkaBrokers      = "KAFKA_BROKERS"
	envHTTPAddr          = "HTTP_ADDR"
	envRecomputeInterval = "RECOMPUTE_INTERVAL"
	envRelayInterval     = "OUTBOX_RELAY_INTERVAL"
	envMaxAttempts       = "OUTBOX_MAX_ATTEMPTS"
	envShutdownDrainWait = "SHUTDOWN_DRAIN_DELAY"
	envLogLevel          = "LOG_LEVEL"

	envVendorGroup   = "VENDOR_CONNECTIVITY_CONSUMER_GROUP"
	envShippingGroup = "SHIPPING_AND_TRACKING_CONSUMER_GROUP"
	envPromiseGroup  = "DELIVERY_PROMISE_CONSUMER_GROUP"
	envOfferGroup    = "OFFER_AVAILABILITY_CONSUMER_GROUP"
	envTopologyGroup = "NODE_TOPOLOGY_CONSUMER_GROUP"
)

// Defaults of the optional settings.
const (
	defaultHTTPAddr          = ":8080"
	defaultRecomputeInterval = time.Minute
	defaultShutdownDrain     = 5 * time.Second
	shutdownTimeout          = 10 * time.Second
	workerDrainTimeout       = 10 * time.Second
	defaultReadHeaderLimit   = 5 * time.Second
)

// ErrNoBrokers is the boot error of an unset or blank KAFKA_BROKERS.
var ErrNoBrokers = errors.New("missing required environment variable: " + envKafkaBrokers + " (comma-separated host:port list)")

// config is the validated environment.
type config struct {
	databaseURL       string
	migrationsURL     string
	brokers           []string
	groups            inboundkafka.Groups
	addr              string
	recomputeInterval time.Duration
	relayInterval     time.Duration
	maxAttempts       int
	drainDelay        time.Duration
	logLevel          string
}

// brokerList is KAFKA_BROKERS as the comma-separated string the relay sink takes.
func (c config) brokerList() string { return strings.Join(c.brokers, ",") }

// loadConfig validates the environment before anything is opened or bound.
// Every boot error names the variable.
func loadConfig(getenv func(string) string, logger *slog.Logger) (config, error) {
	dbURL, err := postgres.DatabaseURL(getenv)
	if err != nil {
		return config{}, err
	}
	brokers := splitBrokers(getenv(envKafkaBrokers))
	if len(brokers) == 0 {
		return config{}, ErrNoBrokers
	}
	groups, err := loadGroups(getenv)
	if err != nil {
		return config{}, err
	}
	return config{
		databaseURL:       dbURL,
		migrationsURL:     postgres.MigrationsURL(getenv, dbURL),
		brokers:           brokers,
		groups:            groups,
		addr:              orDefault(getenv(envHTTPAddr), defaultHTTPAddr),
		recomputeInterval: parseInterval(envRecomputeInterval, getenv(envRecomputeInterval), defaultRecomputeInterval, logger),
		relayInterval:     parseInterval(envRelayInterval, getenv(envRelayInterval), outboxrelay.DefaultInterval, logger),
		maxAttempts:       outboxMaxAttempts(getenv(envMaxAttempts), logger),
		drainDelay:        shutdownDrainDelay(getenv(envShutdownDrainWait), logger),
		logLevel:          getenv(envLogLevel),
	}, nil
}

// loadGroups reads the five consumer group ids, each mandatory.
func loadGroups(getenv func(string) string) (inboundkafka.Groups, error) {
	var g inboundkafka.Groups
	for _, f := range []struct {
		env string
		dst *string
	}{
		{envVendorGroup, &g.VendorConnectivity}, {envShippingGroup, &g.ShippingAndTracking},
		{envPromiseGroup, &g.DeliveryPromise}, {envOfferGroup, &g.OfferAvailability},
		{envTopologyGroup, &g.NodeTopology},
	} {
		v, err := postgres.RequireEnv(getenv, f.env)
		if err != nil {
			return inboundkafka.Groups{}, err
		}
		*f.dst = v
	}
	return g, nil
}

// splitBrokers trims every entry of the comma-separated list and drops blanks.
func splitBrokers(raw string) []string {
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// parseInterval reads a positive interval (a Go duration or plain seconds),
// defaulting when unset, malformed or non-positive.
func parseInterval(name, raw string, fallback time.Duration, logger *slog.Logger) time.Duration {
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if secs, serr := strconv.ParseFloat(raw, 64); serr == nil {
			d, err = time.Duration(secs*float64(time.Second)), nil
		}
	}
	if err != nil || d <= 0 {
		logger.Warn("invalid "+name+"; using the default", "value", raw, "default", fallback)
		return fallback
	}
	return d
}

// outboxMaxAttempts reads OUTBOX_MAX_ATTEMPTS: how many times an outbox row may
// fail before the relay excludes it from claims. Unset = the fleet default
// (20); 0 = unlimited; negative or unparsable values log a warning and use the
// default, so a typo never silently removes the cap.
func outboxMaxAttempts(raw string, logger *slog.Logger) int {
	if raw == "" {
		return outboxrelay.DefaultMaxAttempts
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		logger.Warn("invalid "+envMaxAttempts+"; using the default", "value", raw, "default", outboxrelay.DefaultMaxAttempts)
		return outboxrelay.DefaultMaxAttempts
	}
	return n
}

// shutdownDrainDelay reads SHUTDOWN_DRAIN_DELAY: how long shutdown waits after
// flipping /readyz to not-ready before closing the listener ("0" disables;
// unset, negative or unparsable = the default).
func shutdownDrainDelay(raw string, logger *slog.Logger) time.Duration {
	if raw == "" {
		return defaultShutdownDrain
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warn(fmt.Sprintf("ignoring invalid %s", envShutdownDrainWait), "value", raw, "default", defaultShutdownDrain.String())
		return defaultShutdownDrain
	}
	return d
}

// newLogLevel maps LOG_LEVEL (debug|info|warn|warning|error, any case) to a
// slog level; anything else is info.
func newLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
