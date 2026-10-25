package kafka

import (
	"context"
	"errors"
	"testing"
)

func receiptData(shipment string, statuses ...string) map[string]any {
	cartons := make([]map[string]any, len(statuses))
	for i, s := range statuses {
		cartons[i] = map[string]any{"sscc": "00340000123456789" + string(rune('0'+i)), "status": s, "items": []any{}, "complianceCodes": []any{}, "futureField": 1}
	}
	return map[string]any{
		"transactionId": "txn-1", "vendorId": "v-1", "nodeId": "n1", "shipmentId": shipment,
		"receivedAt": at(90), "cartons": cartons, "futureField": "ignored",
	}
}

func TestInboundReceiptAndShipmentAreStoredFromTheRealContracts(t *testing.T) {
	r := newRig()
	vendor := r.consumer(VendorConnectivityTopic, VendorHandlers)
	seller := r.consumer(SellerInboundTopic, SellerInboundHandlers)
	ctx := context.Background()

	if err := vendor.HandleMessage(ctx, event(t, "r1", TypeInboundReceiptSubmitted, receiptData("shp-1", "RECEIVED", "DAMAGED", "MISSING"))); err != nil {
		t.Fatal(err)
	}
	shipped := map[string]any{"shipmentId": "shp-1", "planId": "p", "sellerId": "s", "nodeId": "n1", "shippedAt": at(0), "futureField": []int{1}}
	if err := seller.HandleMessage(ctx, event(t, "s1", TypeInboundShipmentShipped, shipped)); err != nil {
		t.Fatal(err)
	}
	if got := r.store.Counts(); got.Receipts != 1 || got.Shipments != 1 || got.Scorecards != 0 || got.Processed != 2 {
		t.Fatalf("counts %+v", got)
	}
	obs, err := r.store.Inbound().Observations(ctx, nil, t0.Add(-1))
	if err != nil || len(obs) != 0 {
		t.Fatalf("no node asked: %v %v", obs, err)
	}
}

func TestInboundTypesAreFullTypesOnTheRightTopics(t *testing.T) {
	if TypeInboundReceiptSubmitted != "com.retailnetwork.partner.vendor-connectivity.inboundreceipt.InboundReceiptSubmitted" {
		t.Fatal(TypeInboundReceiptSubmitted)
	}
	if TypeInboundShipmentShipped != "com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentShipped" {
		t.Fatal(TypeInboundShipmentShipped)
	}
	if SellerInboundTopic != "retailnetwork.seller-inbound.events" {
		t.Fatal(SellerInboundTopic)
	}
	r := newRig()
	if _, ok := VendorHandlers(r.intake, nil)[TypeInboundReceiptSubmitted]; !ok {
		t.Fatal("the vendor consumer must handle the receipt")
	}
	if len(SellerInboundHandlers(r.intake, nil)) != 1 {
		t.Fatal("the seller-inbound consumer handles exactly InboundShipmentShipped")
	}
}

func TestAnInboundPayloadThatCanNeverBeAppliedIsPoison(t *testing.T) {
	r := newRig()
	vendor := r.consumer(VendorConnectivityTopic, VendorHandlers)
	seller := r.consumer(SellerInboundTopic, SellerInboundHandlers)
	ctx := context.Background()
	for name, f := range map[string]func() error{
		"receipt of the wrong shape": func() error {
			return vendor.HandleMessage(ctx, event(t, "p1", TypeInboundReceiptSubmitted, []byte(`{"cartons": 3}`)))
		},
		"receipt without a shipment": func() error {
			d := receiptData("", "RECEIVED")
			return vendor.HandleMessage(ctx, event(t, "p2", TypeInboundReceiptSubmitted, d))
		},
		"receipt without an instant": func() error {
			return vendor.HandleMessage(ctx, event(t, "p3", TypeInboundReceiptSubmitted, map[string]any{"nodeId": "n1", "shipmentId": "s"}))
		},
		"shipment without an instant": func() error {
			return seller.HandleMessage(ctx, event(t, "p4", TypeInboundShipmentShipped, map[string]any{"nodeId": "n1", "shipmentId": "s"}))
		},
		"shipment of the wrong shape": func() error {
			return seller.HandleMessage(ctx, event(t, "p5", TypeInboundShipmentShipped, []byte(`{"shippedAt": 12}`)))
		},
	} {
		if err := f(); !errors.Is(err, errPoison) {
			t.Errorf("%s: err = %v, want poison", name, err)
		}
	}
	if got := r.store.Counts(); got.Processed != 0 || got.Receipts != 0 || got.Shipments != 0 {
		t.Fatalf("a poison message changes nothing: %+v", got)
	}
}

func TestAReceiptWithoutCartonsIsStored(t *testing.T) {
	r := newRig()
	vendor := r.consumer(VendorConnectivityTopic, VendorHandlers)
	if err := vendor.HandleMessage(context.Background(), event(t, "r0", TypeInboundReceiptSubmitted, receiptData("shp-0"))); err != nil {
		t.Fatal(err)
	}
	if got := r.store.Counts(); got.Receipts != 1 {
		t.Fatalf("counts %+v", got)
	}
}
