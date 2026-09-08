package main

import "testing"

func TestOutboundDeliveryStateDescribesServerAcknowledgement(t *testing.T) {
	if got := outboundDeliveryState(); got != "server_acknowledged" {
		t.Fatalf("outboundDeliveryState() = %q, want server_acknowledged", got)
	}
}
