package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	_ "modernc.org/sqlite"
)

// Ported from GoClaw (internal/channels/whatsapp/auth_test.go).
//
// The core pattern: a real whatsmeow sqlstore on an in-memory SQLite
// database plus a synthetic ADVSignedDeviceIdentity, so device/pairing
// lifecycle logic is tested without touching the WhatsApp network.

func newAuthTestDB(t *testing.T) (*sql.DB, *sqlstore.Container) {
	t.Helper()

	ctx := context.Background()
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	container := sqlstore.NewWithDB(db, "sqlite3", nil)
	if err := container.Upgrade(ctx); err != nil {
		t.Fatalf("upgrade whatsmeow store: %v", err)
	}

	return db, container
}

func saveWhatsAppTestDevice(t *testing.T, container *sqlstore.Container, user string) types.JID {
	t.Helper()

	jid := types.NewJID(user, types.DefaultUserServer)
	device := container.NewDevice()
	device.ID = &jid
	device.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte{},
		AccountSignatureKey: make([]byte, 32),
		AccountSignature:    make([]byte, 64),
		DeviceSignature:     make([]byte, 64),
	}
	if err := device.Save(context.Background()); err != nil {
		t.Fatalf("save test device %v: %v", jid, err)
	}
	return jid
}

// wireTestClient attaches an externally built container/device to a
// WhatsmeowClient, mirroring the post-open steps of initStore.
func wireTestClient(t *testing.T, container *sqlstore.Container) *WhatsmeowClient {
	t.Helper()

	w := NewWhatsmeowClient(t.TempDir(), false)
	w.container = container
	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		t.Fatalf("get first device: %v", err)
	}
	w.device = device
	return w
}

// --- initStore ---

func TestInitStoreCreatesFreshUnpairedDevice(t *testing.T) {
	w := NewWhatsmeowClient(t.TempDir(), false)
	if err := w.initStore(context.Background()); err != nil {
		t.Fatalf("initStore() error = %v", err)
	}
	if w.device == nil {
		t.Fatal("initStore() device = nil, want fresh device")
	}
	if w.device.ID != nil {
		t.Fatalf("fresh device Store.ID = %v, want nil (unpaired)", w.device.ID)
	}
}

func TestInitStoreLoadsExistingPairedDevice(t *testing.T) {
	// Single-account semantics: initStore must return the first stored
	// device (paired), not create a fresh one beside it.
	dir := t.TempDir()

	db, err := sql.Open("sqlite", sessionDSN(dir))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	container := sqlstore.NewWithDB(db, "sqlite3", nil)
	if err := container.Upgrade(context.Background()); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	wantJID := saveWhatsAppTestDevice(t, container, "15550000001")
	_ = db.Close()

	w := NewWhatsmeowClient(dir, false)
	if err := w.initStore(context.Background()); err != nil {
		t.Fatalf("initStore() error = %v", err)
	}
	if w.device.ID == nil {
		t.Fatal("initStore() device.ID = nil, want the stored paired device")
	}
	if *w.device.ID != wantJID {
		t.Fatalf("initStore() device.ID = %v, want %v", *w.device.ID, wantJID)
	}
}

// --- Connect ---

func TestConnectUnpairedReturnsErrNotPaired(t *testing.T) {
	w := NewWhatsmeowClient(t.TempDir(), false)
	err := w.Connect(context.Background())
	if !errors.Is(err, ErrNotPaired) {
		t.Fatalf("Connect() error = %v, want ErrNotPaired", err)
	}
	if s := w.State(); s.Paired || s.Connected || s.AwaitingQR {
		t.Errorf("Connect(unpaired) state = %+v, want zero PairState", s)
	}
}

// --- newClient ---

func TestNewClientWiresPairedDeviceAndAutoReconnect(t *testing.T) {
	_, container := newAuthTestDB(t)
	jid := saveWhatsAppTestDevice(t, container, "15550000002")

	w := wireTestClient(t, container)
	w.newClient()

	if w.client == nil {
		t.Fatal("newClient() client = nil")
	}
	if w.client.Store == nil || w.client.Store.ID == nil {
		t.Fatal("newClient() client.Store.ID = nil, want paired device")
	}
	if *w.client.Store.ID != jid {
		t.Fatalf("client.Store.ID = %v, want %v", *w.client.Store.ID, jid)
	}
	if !w.client.EnableAutoReconnect {
		t.Error("EnableAutoReconnect = false, want true (whatsmeow handles reconnects)")
	}
}

// --- device deletion forces fresh pairing (GoClaw: TestReauthReplacesDeletedClientStore) ---

func TestDeviceDeleteForcesFreshPairing(t *testing.T) {
	_, container := newAuthTestDB(t)
	saveWhatsAppTestDevice(t, container, "15557654321")

	w := wireTestClient(t, container)
	w.newClient()

	staleStore := w.client.Store
	if staleStore.ID == nil {
		t.Fatal("precondition: paired device expected")
	}

	// Deleting the device store is what logout/reauth does before re-pairing.
	if err := staleStore.Delete(context.Background()); err != nil {
		t.Fatalf("Store.Delete() error = %v", err)
	}
	if !staleStore.Deleted {
		t.Fatal("Store.Delete() did not mark the store deleted")
	}

	// A replacement device must start unpaired.
	fresh := container.NewDevice()
	if fresh.ID != nil {
		t.Fatalf("fresh device Store.ID = %v, want nil for QR login", fresh.ID)
	}
	if fresh.Deleted {
		t.Fatal("fresh device already marked deleted")
	}
}

// --- state machine ---

func TestStateTransitionsOnPairAndDisconnect(t *testing.T) {
	w := NewWhatsmeowClient(t.TempDir(), false)

	// Simulate the event-handler state transitions without a network:
	// PairSuccess → Connected → Disconnected.
	w.mu.Lock()
	w.state.Paired = true
	w.state.AwaitingQR = false
	w.state.DeviceJID = "15550000003@s.whatsapp.net"
	w.mu.Unlock()

	w.mu.Lock()
	w.state.Connected = true
	w.mu.Unlock()

	if s := w.State(); !s.Paired || !s.Connected || s.AwaitingQR {
		t.Errorf("paired+connected state = %+v", s)
	}

	w.mu.Lock()
	w.state.Connected = false
	w.mu.Unlock()

	if s := w.State(); !s.Paired || s.Connected {
		t.Errorf("disconnected state = %+v, want paired but not connected", s)
	}
}

// --- Logout state reset (store-level; no network) ---

func TestLogoutResetsPairState(t *testing.T) {
	w := NewWhatsmeowClient(t.TempDir(), false)

	// Pre-populate state as if paired and connected.
	w.mu.Lock()
	w.state = PairState{
		Paired:    true,
		Connected: true,
		DeviceJID: "15550000004@s.whatsapp.net",
	}
	w.mu.Unlock()

	// Without a live client, Logout must still reset state and re-init a
	// fresh store for the next login.
	if err := w.Logout(context.Background()); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}

	if s := w.State(); s.Paired || s.Connected || s.AwaitingQR || s.DeviceJID != "" {
		t.Errorf("Logout() state = %+v, want zero PairState", s)
	}
	if w.device == nil {
		t.Fatal("Logout() did not re-init a fresh device for next login")
	}
	if w.device.ID != nil {
		t.Fatalf("Logout() fresh device ID = %v, want nil", w.device.ID)
	}
}
