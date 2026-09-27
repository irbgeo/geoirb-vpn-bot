package store

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// testStore connects to MONGO_URI (default localhost), wipes the test
// database and skips the test when Mongo is not reachable.
func testStore(t *testing.T) *Store {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	s, err := Connect(
		ctx,
		ConnectInput{
			URI:       uri,
			DBName:    "geoirb_vpn_test",
			SecretKey: testKey,
		},
	)
	if err != nil {
		t.Skipf("mongo not reachable at %s: %v", uri, err)
	}
	require.NoError(t, s.db.Drop(ctx))
	require.NoError(t, s.ensureIndexes(ctx))
	t.Cleanup(func() { _ = s.Disconnect(context.Background()) })
	return s
}

// ts is a Mongo-friendly timestamp: UTC, millisecond precision.
func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestUserRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	got, err := s.Users.Get(ctx, 42)
	require.NoError(t, err)
	require.Nil(t, got)

	u := &service.User{
		ID:        42,
		Username:  "alice",
		Role:      service.RoleAdmin,
		TrialUsed: true,
		CreatedAt: ts("2026-09-27T10:00:00Z"),
	}
	created, err := s.Users.Register(ctx, u)
	require.NoError(t, err)
	require.Equal(t, u, created, "a new user is stored as given")

	again, err := s.Users.Register(
		ctx,
		&service.User{
			ID:        42,
			Username:  "alice2",
			Role:      service.RoleUser,
			CreatedAt: ts("2026-10-01T10:00:00Z"),
		},
	)
	require.NoError(t, err)
	u.Username = "alice2"
	require.Equal(t, u, again, "an existing user only gets the new username")

	got, err = s.Users.Get(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, u, got)
}

func TestUserWithoutRoleIsPlainUser(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, err := s.Users.coll.InsertOne(ctx, byID(int64(7)))
	require.NoError(t, err)

	got, err := s.Users.Get(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, service.RoleUser, got.Role)
}

func TestUsersByRole(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for id, r := range map[int64]service.Role{
		1: service.RoleAdmin,
		2: service.RoleUser,
		3: service.RoleAdmin,
	} {
		_, err := s.Users.Register(
			ctx,
			&service.User{
				ID:   id,
				Role: r,
			},
		)
		require.NoError(t, err)
	}

	got, err := s.Users.ByRole(ctx, service.RoleAdmin)
	require.NoError(t, err)
	ids := []int64{}
	for _, u := range got {
		ids = append(ids, u.ID)
	}
	require.ElementsMatch(t, []int64{1, 3}, ids)
}

func TestUserList(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := int64(1); i <= 3; i++ {
		_, err := s.Users.Register(
			ctx,
			&service.User{
				ID:        i,
				CreatedAt: ts("2026-09-27T10:00:00Z").Add(time.Duration(i) * time.Hour),
			},
		)
		require.NoError(t, err)
	}

	page, total, err := s.Users.List(
		ctx,
		service.Page{
			Skip:  1,
			Limit: 1,
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Len(t, page, 1)
	require.Equal(t, int64(2), page[0].ID, "newest first: 3, 2, 1")
}

func testPeer() *service.Peer {
	return &service.Peer{
		PublicKey:  "PUB=",
		ServerID:   "geoirb-vpn",
		UserID:     42,
		Name:       "tg:alice",
		IP:         "10.8.1.10",
		PrivateKey: "PRIV=",
		PSK:        "PSK=",
		Enabled:    true,
		ExpiresAt:  ts("2026-10-27T10:00:00Z"),
		Reminded3d: true,
		CreatedAt:  ts("2026-09-27T10:00:00Z"),
	}
}

func TestPeerRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	got, err := s.Peers.Get(ctx, "PUB=")
	require.NoError(t, err)
	require.Nil(t, got)

	p := testPeer()
	require.NoError(t, s.Peers.Save(ctx, p))
	got, err = s.Peers.Get(ctx, "PUB=")
	require.NoError(t, err)
	require.Equal(t, p, got)

	p.ExpiresAt = time.Time{}
	require.NoError(t, s.Peers.Save(ctx, p))
	got, err = s.Peers.Get(ctx, "PUB=")
	require.NoError(t, err)
	require.True(t, got.ExpiresAt.IsZero(), "zero ExpiresAt means never expires")

	require.NoError(t, s.Peers.Delete(ctx, "PUB="))
	got, err = s.Peers.Get(ctx, "PUB=")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestPeerSecretsEncryptedAtRest(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	require.NoError(t, s.Peers.Save(ctx, testPeer()))

	raw, err := s.Peers.coll.FindOne(ctx, byID("PUB=")).Raw()
	require.NoError(t, err)
	require.NotContains(t, raw.String(), "PRIV=")
	require.NotContains(t, raw.String(), "PSK=")

	got, err := s.Peers.Get(ctx, "PUB=")
	require.NoError(t, err)
	require.Equal(t, "PRIV=", got.PrivateKey)
	require.Equal(t, "PSK=", got.PSK)
}

func TestConnectRejectsBadSecretKey(t *testing.T) {
	_, err := Connect(
		context.Background(),
		ConnectInput{
			URI:       "mongodb://localhost:1",
			DBName:    "x",
			SecretKey: []byte("short"),
		},
	)
	require.ErrorContains(t, err, "32 bytes", "checked before dialing")
}

func TestPeerIPUniquePerServer(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	require.NoError(t, s.Peers.Save(ctx, testPeer()))

	clash := testPeer()
	clash.PublicKey = "OTHER="
	require.Error(t, s.Peers.Save(ctx, clash), "same server + IP")

	clash.ServerID = "another-server"
	require.NoError(t, s.Peers.Save(ctx, clash), "same IP on another server is fine")
}

func TestPeersByUserAndServer(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testPeer()
	b := testPeer()
	b.PublicKey = "PUB2="
	b.IP = "10.8.1.11"
	b.UserID = 7
	require.NoError(t, s.Peers.Save(ctx, a))
	require.NoError(t, s.Peers.Save(ctx, b))

	byUser, err := s.Peers.ByUser(ctx, 42)
	require.NoError(t, err)
	require.Len(t, byUser, 1)
	require.Equal(t, "PUB=", byUser[0].PublicKey)

	byServer, err := s.Peers.ByServer(ctx, "geoirb-vpn")
	require.NoError(t, err)
	require.Len(t, byServer, 2)
}

func testPayment() *service.Payment {
	return &service.Payment{
		ChargeID:  "charge-1",
		UserID:    42,
		PeerKey:   "PUB=",
		Stars:     150,
		Days:      30,
		CreatedAt: ts("2026-09-27T10:00:00Z"),
	}
}

func TestPaymentAddIsIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	added, err := s.Payments.Add(ctx, testPayment())
	require.NoError(t, err)
	require.True(t, added)

	added, err = s.Payments.Add(ctx, testPayment())
	require.NoError(t, err)
	require.False(t, added, "same charge ID twice")
}

func TestPaymentRoundTripAndQueries(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	got, err := s.Payments.Get(ctx, "charge-1")
	require.NoError(t, err)
	require.Nil(t, got)

	p := testPayment()
	_, err = s.Payments.Add(ctx, p)
	require.NoError(t, err)
	old := testPayment()
	old.ChargeID = "charge-0"
	old.CreatedAt = ts("2026-08-01T10:00:00Z")
	_, err = s.Payments.Add(ctx, old)
	require.NoError(t, err)

	p.Applied = true
	p.RefundedAt = ts("2026-09-28T10:00:00Z")
	require.NoError(t, s.Payments.Save(ctx, p))
	got, err = s.Payments.Get(ctx, "charge-1")
	require.NoError(t, err)
	require.Equal(t, p, got)

	byUser, err := s.Payments.ByUser(ctx, 42)
	require.NoError(t, err)
	require.Len(t, byUser, 2)
	require.Equal(t, "charge-1", byUser[0].ChargeID, "newest first")

	since, err := s.Payments.Since(ctx, ts("2026-09-01T00:00:00Z"))
	require.NoError(t, err)
	require.Len(t, since, 1)
	require.Equal(t, "charge-1", since[0].ChargeID)
}

func TestRegisterKeepsHandSetFields(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, err := s.Users.coll.InsertOne(
		ctx,
		bson.M{
			"_id":      int64(7),
			"role":     "unlimited",
			"username": "old",
			"note":     "set by hand",
		},
	)
	require.NoError(t, err)

	u, err := s.Users.Register(
		ctx,
		&service.User{
			ID:       7,
			Username: "new",
			Role:     service.RoleUser,
		},
	)
	require.NoError(t, err)
	require.Equal(t, service.RoleUnlimited, u.Role, "a role set by hand is not written over")

	var raw bson.M
	require.NoError(t, s.Users.coll.FindOne(ctx, byID(int64(7))).Decode(&raw))
	require.Equal(t, "set by hand", raw["note"], "unknown fields survive")
	require.Equal(t, "new", raw["username"])
}

func TestSetTrialUsed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, err := s.Users.Register(
		ctx,
		&service.User{
			ID:   7,
			Role: service.RoleUser,
		},
	)
	require.NoError(t, err)

	require.NoError(t, s.Users.SetTrialUsed(ctx, 7))
	got, err := s.Users.Get(ctx, 7)
	require.NoError(t, err)
	require.True(t, got.TrialUsed)
	require.Equal(t, service.RoleUser, got.Role)
}

func TestPeerListsSkipUnreadableRowsButReserveTheirIPs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	require.NoError(t, s.Peers.Save(ctx, testPeer()))
	broken := testPeer()
	broken.PublicKey = "BROKEN="
	broken.IP = "10.8.1.11"
	require.NoError(t, s.Peers.Save(ctx, broken))
	_, err := s.Peers.coll.UpdateOne(
		ctx,
		byID("BROKEN="),
		bson.M{
			"$set": bson.M{
				"psk": "v1:garbage",
			},
		},
	)
	require.NoError(t, err)

	ps, err := s.Peers.ByServer(ctx, "geoirb-vpn")
	require.NoError(t, err, "one bad row must not stop the rest")
	require.Len(t, ps, 1)

	ips, err := s.Peers.ServerIPs(ctx, "geoirb-vpn")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"10.8.1.10", "10.8.1.11"}, ips, "its IP stays taken")
}

func TestConnectRefusesAWrongSecretKey(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.Peers.Save(context.Background(), testPeer()))

	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	_, err := Connect(
		context.Background(),
		ConnectInput{
			URI:       uri,
			DBName:    "geoirb_vpn_test",
			SecretKey: bytes.Repeat([]byte{9}, 32),
		},
	)
	require.ErrorContains(t, err, "DB_SECRET_KEY")
}
