package repocontract

import (
	"context"
	"reflect"
	"testing"

	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

var inboundCases = []named{
	{"the first shipment instant wins and a repeat is not new", inboundShipmentFirstWins},
	{"the first receipt of a shipment wins and a repeat is not new", inboundReceiptFirstWins},
	{"a receipt is joined to its shipment and a parked receipt is not returned", inboundJoin},
	{"observations are filtered by node and by the instant, strictly after", inboundFilters},
	{"saves roll back with their unit of work", inboundRollsBack},
}

func shipment(id, node string, minutes int) scorecard.InboundShipment {
	return scorecard.InboundShipment{ShipmentID: id, NodeID: scorecard.NodeID(node), ShippedAt: At(minutes)}
}

func receipt(id, node string, minutes, total, received int) scorecard.InboundReceipt {
	return scorecard.InboundReceipt{ShipmentID: id, NodeID: scorecard.NodeID(node), ReceivedAt: At(minutes), CartonsTotal: total, CartonsReceived: received}
}

func observations(t *testing.T, f Fixture, minutes int, nodes ...string) map[scorecard.NodeID][]scorecard.InboundObservation {
	t.Helper()
	ids := make([]scorecard.NodeID, len(nodes))
	for i, n := range nodes {
		ids[i] = scorecard.NodeID(n)
	}
	got, err := f.InboundReader.Observations(context.Background(), ids, At(minutes))
	noErr(t, err)
	return got
}

func inboundShipmentFirstWins(t *testing.T, f Fixture) {
	ctx := context.Background()
	isNew, err := f.Inbound.SaveShipment(ctx, shipment("shp-1", "node-1", 0))
	noErr(t, err)
	if !isNew {
		t.Fatal("the first save must be new")
	}
	isNew, err = f.Inbound.SaveShipment(ctx, shipment("shp-1", "node-1", -500))
	noErr(t, err)
	if isNew {
		t.Fatal("a repeat must not be new")
	}
	noErr(t, saveReceipt(f, receipt("shp-1", "node-1", 90, 1, 1)))
	got := observations(t, f, -1000, "node-1")["node-1"]
	if len(got) != 1 || got[0].Minutes != 90 {
		t.Fatalf("the stored instant must be the first one (90 minutes): %+v", got)
	}
}

func saveReceipt(f Fixture, r scorecard.InboundReceipt) error {
	_, err := f.Inbound.SaveReceipt(context.Background(), r)
	return err
}

func inboundReceiptFirstWins(t *testing.T, f Fixture) {
	ctx := context.Background()
	isNew, err := f.Inbound.SaveReceipt(ctx, receipt("shp-1", "node-1", 60, 4, 3))
	noErr(t, err)
	if !isNew {
		t.Fatal("the first receipt must be new")
	}
	isNew, err = f.Inbound.SaveReceipt(ctx, receipt("shp-1", "node-1", 70, 9, 9))
	noErr(t, err)
	if isNew {
		t.Fatal("a second receipt of the shipment must not be new")
	}
	_, err = f.Inbound.SaveShipment(ctx, shipment("shp-1", "node-1", 0))
	noErr(t, err)
	want := []scorecard.InboundObservation{{ReceivedAt: At(60), Minutes: 60, CartonsTotal: 4, CartonsReceived: 3}}
	if got := observations(t, f, -1, "node-1")["node-1"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("observations = %+v, want %+v", got, want)
	}
}

func inboundJoin(t *testing.T, f Fixture) {
	ctx := context.Background()
	_, err := f.Inbound.SaveReceipt(ctx, receipt("shp-1", "node-1", 60, 2, 2))
	noErr(t, err)
	if got := observations(t, f, -1, "node-1"); len(got) != 0 {
		t.Fatalf("a parked receipt must not be returned: %+v", got)
	}
	_, err = f.Inbound.SaveShipment(ctx, shipment("shp-1", "node-1", 15))
	noErr(t, err)
	got := observations(t, f, -1, "node-1")["node-1"]
	if len(got) != 1 || got[0].Minutes != 45 {
		t.Fatalf("the parked receipt must join when the shipment arrives: %+v", got)
	}
	_, err = f.Inbound.SaveShipment(ctx, shipment("shp-2", "node-1", 10))
	noErr(t, err)
	if got := observations(t, f, -1, "node-1")["node-1"]; len(got) != 1 {
		t.Fatalf("a shipment without receipt adds nothing: %+v", got)
	}
}

func inboundFilters(t *testing.T, f Fixture) {
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		_, err := f.Inbound.SaveShipment(ctx, shipment(id, "node-1", 0))
		noErr(t, err)
	}
	noErr(t, saveReceipt(f, receipt("a", "node-1", 10, 1, 1)))
	noErr(t, saveReceipt(f, receipt("b", "node-2", 20, 1, 0)))
	noErr(t, saveReceipt(f, receipt("c", "node-1", 30, 1, 1)))
	got := observations(t, f, 10, "node-1", "node-2")
	if len(got["node-1"]) != 1 || got["node-1"][0].Minutes != 30 || len(got["node-2"]) != 1 {
		t.Fatalf("strictly after 10, per node: %+v", got)
	}
	if only := observations(t, f, -1, "node-2"); len(only) != 1 || len(only["node-2"]) != 1 {
		t.Fatalf("only the asked node is returned: %+v", only)
	}
	if none := observations(t, f, -1); len(none) != 0 {
		t.Fatalf("no node asked, nothing returned: %+v", none)
	}
}

func inboundRollsBack(t *testing.T, f Fixture) {
	ctx := context.Background()
	err := f.UoW.Do(ctx, func(ctx context.Context) error {
		_, err := f.Inbound.SaveShipment(ctx, shipment("shp-1", "node-1", 0))
		noErr(t, err)
		_, err = f.Inbound.SaveReceipt(ctx, receipt("shp-1", "node-1", 5, 1, 1))
		noErr(t, err)
		return errBoom
	})
	wantErr(t, err, errBoom)
	if got := observations(t, f, -1, "node-1"); len(got) != 0 {
		t.Fatalf("a rolled back unit stored: %+v", got)
	}
}
