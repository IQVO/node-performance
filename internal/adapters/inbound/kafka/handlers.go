package kafka

import (
	"context"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/node-performance/internal/application/usecases"
)

// Consumed topics: the producers' integration topics (their contracts are
// restated in apis/asyncapi.yaml, never imported).
const (
	VendorConnectivityTopic   = "retailnetwork.vendor-connectivity.events"
	ShippingAndTrackingTopic  = "retailnetwork.shipping-and-tracking.events"
	DeliveryPromiseTopic      = "retailnetwork.delivery-promise.events"
	OfferAvailabilityTopic    = "retailnetwork.offer-availability.events"
	NodeTopologyTopic         = "retailnetwork.node-topology.events"
	vendorTypePrefix          = "com.retailnetwork.partner.vendor-connectivity."
	shippingTypePrefix        = "com.retailnetwork.logistics.shipping-and-tracking."
	deliveryPromiseTypePrefix = "com.retailnetwork.commerce.delivery-promise."
	offerAvailabilityPrefix   = "com.retailnetwork.commerce.offer-availability."
	nodeTopologyTypePrefix    = "com.retailnetwork.logistics.node-topology."
	SellerInboundTopic        = "retailnetwork.seller-inbound.events"
	sellerInboundTypePrefix   = "com.retailnetwork.partner.seller-inbound."
)

// The FULL CloudEvents types this service reacts to.
const (
	TypeVendorOnboarded                = vendorTypePrefix + "vendoraccount.VendorOnboarded"
	TypePurchaseOrderIssued            = vendorTypePrefix + "purchaseorder.PurchaseOrderIssued"
	TypePurchaseOrderAccepted          = vendorTypePrefix + "purchaseorder.PurchaseOrderAccepted"
	TypePurchaseOrderRejected          = vendorTypePrefix + "purchaseorder.PurchaseOrderRejected"
	TypePurchaseOrderExpired           = vendorTypePrefix + "purchaseorder.PurchaseOrderExpired"
	TypeVendorShipmentConfirmed        = vendorTypePrefix + "purchaseorder.VendorShipmentConfirmed"
	TypeSubmissionProcessed            = vendorTypePrefix + "submission.SubmissionProcessed"
	TypePackageHandedOver              = shippingTypePrefix + "journey.PackageHandedOver"
	TypePromiseKept                    = deliveryPromiseTypePrefix + "promise.PromiseKept"
	TypePromiseBroken                  = deliveryPromiseTypePrefix + "promise.PromiseBroken"
	TypeInventoryFeedApplied           = offerAvailabilityPrefix + "inventoryfeed.InventoryFeedApplied"
	TypeInboundReceiptSubmitted        = vendorTypePrefix + "inboundreceipt.InboundReceiptSubmitted"
	TypeInboundShipmentShipped         = sellerInboundTypePrefix + "inboundshipment.InboundShipmentShipped"
	TypeNodeRegistered                 = nodeTopologyTypePrefix + "node.NodeRegistered"
	TypeNodeOperationalProfileDeclared = nodeTopologyTypePrefix + "node.NodeOperationalProfileDeclared"
)

// Groups are the five consumer group ids, each from its own mandatory env var.
type Groups struct {
	VendorConnectivity  string
	ShippingAndTracking string
	DeliveryPromise     string
	OfferAvailability   string
	NodeTopology        string
	SellerInbound       string
}

// ref builds the usecases envelope of a consumed event.
func ref(e ce.Event) usecases.EventRef {
	return usecases.EventRef{ID: e.ID(), Type: e.Type(), Time: e.Time().UTC()}
}

// The relied-on content of each payload (hand-mirrored from apis/asyncapi.yaml).
type (
	issuedData struct {
		PoNumber       string    `json:"poNumber"`
		NodeID         string    `json:"nodeId"`
		RequiredShipBy time.Time `json:"requiredShipBy"`
		AckBy          time.Time `json:"ackBy"`
	}
	acceptedData struct {
		PoNumber   string    `json:"poNumber"`
		NodeID     string    `json:"nodeId"`
		AcceptedAt time.Time `json:"acceptedAt"`
	}
	rejectedData struct {
		PoNumber   string    `json:"poNumber"`
		NodeID     string    `json:"nodeId"`
		RejectedAt time.Time `json:"rejectedAt"`
	}
	expiredData struct {
		PoNumber string    `json:"poNumber"`
		NodeID   string    `json:"nodeId"`
		AckBy    time.Time `json:"ackBy"`
	}
	shipmentData struct {
		PoNumber       string    `json:"poNumber"`
		NodeID         string    `json:"nodeId"`
		RequiredShipBy time.Time `json:"requiredShipBy"`
	}
	handedOverData struct {
		PoNumber     string    `json:"poNumber"`
		NodeID       string    `json:"nodeId"`
		HandedOverAt time.Time `json:"handedOverAt"`
	}
	keptData struct {
		NodeID      string    `json:"nodeId"`
		DeliveredAt time.Time `json:"deliveredAt"`
	}
	brokenData struct {
		NodeID      string    `json:"nodeId"`
		DeliveredAt time.Time `json:"deliveredAt"`
		Cause       string    `json:"cause"`
	}
	inboundReceiptData struct {
		NodeID     string    `json:"nodeId"`
		ShipmentID string    `json:"shipmentId"`
		ReceivedAt time.Time `json:"receivedAt"`
		Cartons    []struct {
			Status string `json:"status"`
		} `json:"cartons"`
	}
	inboundShippedData struct {
		NodeID     string    `json:"nodeId"`
		ShipmentID string    `json:"shipmentId"`
		ShippedAt  time.Time `json:"shippedAt"`
	}
	feedData struct {
		NodeID    string    `json:"nodeId"`
		AppliedAt time.Time `json:"appliedAt"`
	}
)

// handle adapts a typed payload + an intake method into a HandlerFunc.
func handle[T any](logger *slog.Logger, apply func(ctx context.Context, e ce.Event, d T) (usecases.Outcome, error)) HandlerFunc {
	logger = defaultLogger(logger)
	return func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[T](e)
		if err != nil {
			return err
		}
		outcome, err := apply(ctx, e, d)
		if err != nil {
			return err
		}
		logOutcome(ctx, logger, e, outcome)
		return nil
	}
}

// noEffect acknowledges an event whose type is consumed but changes nothing
// (ADR 0002, A4): only the inbox row is written.
func noEffect(logger *slog.Logger, intake usecases.Intake) HandlerFunc {
	logger = defaultLogger(logger)
	return func(ctx context.Context, e ce.Event) error {
		outcome, err := intake.Acknowledge(ctx, ref(e))
		if err != nil {
			return err
		}
		logOutcome(ctx, logger, e, outcome)
		return nil
	}
}

// VendorHandlers returns the vendor-connectivity handlers.
func VendorHandlers(intake usecases.Intake, logger *slog.Logger) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypePurchaseOrderIssued: handle(logger, func(ctx context.Context, e ce.Event, d issuedData) (usecases.Outcome, error) {
			return intake.PurchaseOrderIssued(ctx, usecases.PurchaseOrderIssued{Ref: ref(e), PoNumber: d.PoNumber, NodeID: d.NodeID, RequiredShipBy: d.RequiredShipBy, AckBy: d.AckBy})
		}),
		TypePurchaseOrderAccepted: handle(logger, func(ctx context.Context, e ce.Event, d acceptedData) (usecases.Outcome, error) {
			return intake.PurchaseOrderAcknowledged(ctx, usecases.PurchaseOrderAcknowledged{Ref: ref(e), PoNumber: d.PoNumber, NodeID: d.NodeID, At: d.AcceptedAt})
		}),
		TypePurchaseOrderRejected: handle(logger, func(ctx context.Context, e ce.Event, d rejectedData) (usecases.Outcome, error) {
			return intake.PurchaseOrderAcknowledged(ctx, usecases.PurchaseOrderAcknowledged{Ref: ref(e), PoNumber: d.PoNumber, NodeID: d.NodeID, At: d.RejectedAt, Rejected: true})
		}),
		TypePurchaseOrderExpired: handle(logger, func(ctx context.Context, e ce.Event, d expiredData) (usecases.Outcome, error) {
			return intake.PurchaseOrderExpired(ctx, usecases.PurchaseOrderExpired{Ref: ref(e), PoNumber: d.PoNumber, NodeID: d.NodeID, AckBy: d.AckBy})
		}),
		TypeVendorShipmentConfirmed: handle(logger, func(ctx context.Context, e ce.Event, d shipmentData) (usecases.Outcome, error) {
			return intake.ShipmentConfirmed(ctx, usecases.ShipmentConfirmed{Ref: ref(e), PoNumber: d.PoNumber, NodeID: d.NodeID, RequiredShipBy: d.RequiredShipBy})
		}),
		TypeInboundReceiptSubmitted: handle(logger, func(ctx context.Context, e ce.Event, d inboundReceiptData) (usecases.Outcome, error) {
			statuses := make([]string, len(d.Cartons))
			for i, c := range d.Cartons {
				statuses[i] = c.Status
			}
			return intake.InboundReceiptSubmitted(ctx, usecases.InboundReceiptSubmitted{Ref: ref(e), ShipmentID: d.ShipmentID, NodeID: d.NodeID, ReceivedAt: d.ReceivedAt, CartonStatuses: statuses})
		}),
		TypeVendorOnboarded:     noEffect(logger, intake),
		TypeSubmissionProcessed: noEffect(logger, intake),
	}
}

// ShippingHandlers returns the shipping-and-tracking handlers.
func ShippingHandlers(intake usecases.Intake, logger *slog.Logger) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypePackageHandedOver: handle(logger, func(ctx context.Context, e ce.Event, d handedOverData) (usecases.Outcome, error) {
			return intake.PackageHandedOver(ctx, usecases.PackageHandedOver{Ref: ref(e), PoNumber: d.PoNumber, NodeID: d.NodeID, HandedOverAt: d.HandedOverAt})
		}),
	}
}

// DeliveryPromiseHandlers returns the delivery-promise handlers.
func DeliveryPromiseHandlers(intake usecases.Intake, logger *slog.Logger) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypePromiseKept: handle(logger, func(ctx context.Context, e ce.Event, d keptData) (usecases.Outcome, error) {
			return intake.PromiseKept(ctx, usecases.PromiseKept{Ref: ref(e), NodeID: d.NodeID, DeliveredAt: d.DeliveredAt})
		}),
		TypePromiseBroken: handle(logger, func(ctx context.Context, e ce.Event, d brokenData) (usecases.Outcome, error) {
			return intake.PromiseBroken(ctx, usecases.PromiseBroken{Ref: ref(e), NodeID: d.NodeID, At: d.DeliveredAt, Cause: d.Cause})
		}),
	}
}

// OfferAvailabilityHandlers returns the offer-availability handlers.
func OfferAvailabilityHandlers(intake usecases.Intake, logger *slog.Logger) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeInventoryFeedApplied: handle(logger, func(ctx context.Context, e ce.Event, d feedData) (usecases.Outcome, error) {
			return intake.InventoryFeedApplied(ctx, usecases.InventoryFeedApplied{Ref: ref(e), NodeID: d.NodeID, AppliedAt: d.AppliedAt})
		}),
	}
}

// NodeTopologyHandlers returns the node-topology handlers: both events are
// consumed without effect (ADR 0002, A4).
func NodeTopologyHandlers(intake usecases.Intake, logger *slog.Logger) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeNodeRegistered:                 noEffect(logger, intake),
		TypeNodeOperationalProfileDeclared: noEffect(logger, intake),
	}
}

// SellerInboundHandlers returns the seller-inbound handlers: only the shipped
// instant is relied on (ADR 0009).
func SellerInboundHandlers(intake usecases.Intake, logger *slog.Logger) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeInboundShipmentShipped: handle(logger, func(ctx context.Context, e ce.Event, d inboundShippedData) (usecases.Outcome, error) {
			return intake.InboundShipmentShipped(ctx, usecases.InboundShipmentShipped{Ref: ref(e), ShipmentID: d.ShipmentID, NodeID: d.NodeID, ShippedAt: d.ShippedAt})
		}),
	}
}

// NewConsumers builds the six consumers over brokers, each under its own group.
// Nothing dials Kafka here.
func NewConsumers(brokers []string, groups Groups, intake usecases.Intake, logger *slog.Logger) []*Consumer {
	logger = defaultLogger(logger)
	return []*Consumer{
		newConsumer("vendor-connectivity consumer", brokers, VendorConnectivityTopic, groups.VendorConnectivity, VendorHandlers(intake, logger), logger),
		newConsumer("shipping-and-tracking consumer", brokers, ShippingAndTrackingTopic, groups.ShippingAndTracking, ShippingHandlers(intake, logger), logger),
		newConsumer("delivery-promise consumer", brokers, DeliveryPromiseTopic, groups.DeliveryPromise, DeliveryPromiseHandlers(intake, logger), logger),
		newConsumer("offer-availability consumer", brokers, OfferAvailabilityTopic, groups.OfferAvailability, OfferAvailabilityHandlers(intake, logger), logger),
		newConsumer("node-topology consumer", brokers, NodeTopologyTopic, groups.NodeTopology, NodeTopologyHandlers(intake, logger), logger),
		newConsumer("seller-inbound consumer", brokers, SellerInboundTopic, groups.SellerInbound, SellerInboundHandlers(intake, logger), logger),
	}
}
