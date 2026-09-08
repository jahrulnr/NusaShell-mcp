package main

import (
	"context"
	"testing"
	"time"
)

func TestConnectionAttemptIsNotAuthenticatedUntilConnectedEvent(t *testing.T) {
	w := NewWhatsmeowClient(t.TempDir(), false)
	defer w.Close()

	w.connectionAttemptStarted("15550000001@s.whatsapp.net")
	if state := w.State(); !state.Paired || state.TransportConnected || state.Connected {
		t.Fatalf("state during connection attempt = %+v, want paired but not transport/authenticated", state)
	}

	w.authenticatedConnectionEstablished()
	if state := w.State(); !state.Paired || !state.TransportConnected || !state.Connected {
		t.Fatalf("state after authenticated connection = %+v, want paired transport+authenticated", state)
	}
}

func TestPairingContextSurvivesMCPRequestCancellation(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	pairingCtx, cancelPairing := newPairingContext(requestCtx)
	defer cancelPairing()
	cancelRequest()

	select {
	case <-pairingCtx.Done():
		t.Fatalf("pairing context ended with MCP request: %v", pairingCtx.Err())
	default:
	}
}

func TestPushEventPreservesBackpressuredEvents(t *testing.T) {
	w := NewWhatsmeowClient(t.TempDir(), false)
	defer w.Close()
	w.eventCh = make(chan any, 1)

	w.pushEvent("first")
	w.pushEvent("second")

	select {
	case got := <-w.eventCh:
		if got != "first" {
			t.Fatalf("first queued event = %v, want first", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first event")
	}
	select {
	case got := <-w.eventCh:
		if got != "second" {
			t.Fatalf("backpressured event = %v, want second", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for backpressured event")
	}
}
