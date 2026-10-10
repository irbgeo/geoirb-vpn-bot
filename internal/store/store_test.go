package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// testStore connects to MONGO_URI (default localhost) and wipes the test
// database. Only a Mongo that does not answer a ping skips the test (and
// with REQUIRE_MONGO=1 even that fails it): any other Connect error is a
// bug in the store and must fail.
func testStore(t *testing.T) *store {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := pingMongo(ctx, uri)
	if err != nil && os.Getenv("REQUIRE_MONGO") == "1" {
		t.Fatalf("mongo not reachable at %s: %v", uri, err)
	}
	if err != nil {
		t.Skipf("mongo not reachable at %s: %v", uri, err)
	}
	s, err := Connect(
		ctx,
		&config.Config{
			MongoURI:  uri,
			MongoDB:   "geoirb_vpn_test",
			SecretKey: testKey,
		},
	)
	require.NoError(t, err)
	require.NoError(t, s.db.Drop(ctx))
	require.NoError(t, s.ensureIndexes(ctx))
	t.Cleanup(func() { _ = s.Disconnect(context.Background()) })
	return s
}

// pingMongo reports whether a Mongo answers at uri.
func pingMongo(ctx context.Context, uri string) error {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background()) //nolint:errcheck
	return client.Ping(ctx, nil)
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

func TestUserListPlainUsersFirstThenUnlimitedThenAdmins(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i, role := range []service.Role{
		service.RoleAdmin,     // id 1, oldest
		service.RoleUnlimited, // id 2
		service.RoleUser,      // id 3
		"",                    // id 4: no role counts as a plain user
		service.RoleUnlimited, // id 5, newest
	} {
		_, err := s.Users.Register(
			ctx,
			&service.User{
				ID:        int64(i + 1),
				Role:      role,
				CreatedAt: ts("2026-09-27T10:00:00Z").Add(time.Duration(i) * time.Hour),
			},
		)
		require.NoError(t, err)
	}

	page, total, err := s.Users.List(
		ctx,
		service.Page{
			Limit: 10,
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(5), total)
	var ids []int64
	for _, u := range page {
		ids = append(ids, u.ID)
	}
	require.Equal(
		t,
		[]int64{
			4,
			3,
			5,
			2,
			1,
		},
		ids,
		"plain users, then unlimited, then admins; newest first inside a role",
	)

	second, _, err := s.Users.List(
		ctx,
		service.Page{
			Skip:  2,
			Limit: 2,
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(5), second[0].ID, "paging follows the same order")
	require.Equal(t, int64(2), second[1].ID)
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
		&config.Config{
			MongoURI:  "mongodb://localhost:1",
			MongoDB:   "x",
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

func TestPeerReplaceSwapsTheKeyOnTheSameIP(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	old := testPeer()
	require.NoError(t, s.Peers.Save(ctx, old))
	fresh := testPeer()
	fresh.PublicKey = "NEW="
	fresh.PrivateKey = "NEWPRIV="
	fresh.PSK = "NEWPSK="

	require.NoError(
		t,
		s.Peers.Replace(
			ctx,
			service.PeerSwap{
				Old: old,
				New: fresh,
			},
		),
		"the unique server+IP index must not stop the swap",
	)
	gone, err := s.Peers.Get(ctx, "PUB=")
	require.NoError(t, err)
	require.Nil(t, gone)
	got, err := s.Peers.Get(ctx, "NEW=")
	require.NoError(t, err)
	require.Equal(t, fresh, got)

	blank := testPeer()
	blank.PSK = ""
	err = s.Peers.Replace(
		ctx,
		service.PeerSwap{
			Old: fresh,
			New: blank,
		},
	)
	require.Error(t, err, "a key without secrets can't replace a working one")
	got, err = s.Peers.Get(ctx, "NEW=")
	require.NoError(t, err)
	require.NotNil(t, got, "nothing was removed")
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

	require.NoError(
		t,
		s.Payments.MarkApplied(
			ctx,
			service.PaymentMark{
				ChargeID: p.ChargeID,
				PeerKey:  "PUB=",
			},
		),
	)
	require.NoError(
		t,
		s.Payments.MarkRefunded(
			ctx,
			service.PaymentMark{
				ChargeID: p.ChargeID,
				At:       ts("2026-09-28T10:00:00Z"),
			},
		),
	)
	p.Applied = true
	p.PeerKey = "PUB="
	p.RefundedAt = ts("2026-09-28T10:00:00Z")
	got, err = s.Payments.Get(ctx, "charge-1")
	require.NoError(t, err)
	require.Equal(t, p, got)

	byUser, err := s.Payments.ByUser(ctx, 42)
	require.NoError(t, err)
	require.Len(t, byUser, 2)
	require.Equal(t, "charge-1", byUser[0].ChargeID, "newest first")

	require.Error(
		t,
		s.Payments.MarkApplied(
			ctx,
			service.PaymentMark{
				ChargeID: "missing",
			},
		),
	)
	require.NoError(
		t,
		s.Payments.MarkRefunded(
			ctx,
			service.PaymentMark{
				ChargeID: "missing",
			},
		),
		"refunding a charge with no record is fine",
	)

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

func TestPeerListsKeepUnreadableRowsWithoutSecrets(t *testing.T) {
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
	require.Len(t, ps, 2, "the bad row is kept, so it still expires")
	var bad *service.Peer
	for _, p := range ps {
		if p.PublicKey == "BROKEN=" {
			bad = p
		}
	}
	require.NotNil(t, bad)
	require.Empty(t, bad.PrivateKey)
	require.Empty(t, bad.PSK)
	require.Equal(t, "10.8.1.11", bad.IP)

	bad.Enabled = false
	require.NoError(t, s.Peers.Save(ctx, bad))
	var raw bson.M
	require.NoError(t, s.Peers.coll.FindOne(ctx, byID("BROKEN=")).Decode(&raw))
	require.Equal(t, "v1:garbage", raw["psk"], "saving the metadata keeps the sealed secrets")
	require.Equal(t, false, raw["enabled"])

	ips, err := s.Peers.ServerIPs(ctx, "geoirb-vpn")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"10.8.1.10", "10.8.1.11"}, ips, "its IP stays taken")
}

func TestConnectAcceptsOneBadRowAmongGoodOnes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	broken := testPeer()
	broken.PublicKey = "BROKEN="
	broken.IP = "10.8.1.9"
	require.NoError(t, s.Peers.Save(ctx, broken)) // first in natural order
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
	require.NoError(t, s.Peers.Save(ctx, testPeer()))

	require.NoError(t, s.Peers.checkKey(ctx), "the key opens the other rows")
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
		&config.Config{
			MongoURI:  uri,
			MongoDB:   "geoirb_vpn_test",
			SecretKey: bytes.Repeat([]byte{9}, 32),
		},
	)
	require.ErrorContains(t, err, "DB_SECRET_KEY")
}

func TestKeyCounts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, id := range []int64{
		1,
		2,
	} {
		_, err := s.Users.Register(
			ctx,
			&service.User{
				ID:   id,
				Role: service.RoleUser,
			},
		)
		require.NoError(t, err)
	}

	require.NoError(t, s.Users.AddKeys(
		ctx,
		service.KeysDelta{
			UserID: 1,
			Delta:  2,
		},
	))
	u, err := s.Users.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 2, u.KeysCount)

	require.NoError(t, s.Users.AddKeys(
		ctx,
		service.KeysDelta{
			UserID: 99,
			Delta:  1,
		},
	))
	missing, err := s.Users.Get(ctx, 99)
	require.NoError(t, err)
	require.Nil(t, missing, "no user row is created by counting")

	require.NoError(t, s.Users.SetKeyCounts(
		ctx,
		map[int64]int{
			2: 3,
		},
	))
	one, err := s.Users.Get(ctx, 1)
	require.NoError(t, err)
	two, err := s.Users.Get(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 0, one.KeysCount, "not in the map: no keys")
	require.Equal(t, 3, two.KeysCount)
}

func TestFeedbackAdd(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	fb := &service.Feedback{
		UserID:    42,
		Username:  "alice",
		Text:      "Добавьте тариф на неделю",
		CreatedAt: ts("2026-09-28T10:00:00Z"),
	}
	require.NoError(t, s.Feedback.Add(ctx, fb))

	var got []feedback
	cur, err := s.Feedback.coll.Find(ctx, matchAll())
	require.NoError(t, err)
	require.NoError(t, cur.All(ctx, &got))
	require.Len(t, got, 1)
	require.Equal(t, int64(42), got[0].UserID)
	require.Equal(t, "alice", got[0].Username)
	require.Equal(t, "Добавьте тариф на неделю", got[0].Text)
	require.True(t, got[0].CreatedAt.Equal(fb.CreatedAt))
}

func TestFeedbackListNewestFirst(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := range 3 {
		require.NoError(
			t,
			s.Feedback.Add(
				ctx,
				&service.Feedback{
					UserID:    int64(i + 1),
					Text:      fmt.Sprintf("отзыв %d", i+1),
					CreatedAt: ts("2026-09-28T10:00:00Z").Add(time.Duration(i) * time.Hour),
				},
			),
		)
	}

	page, total, err := s.Feedback.List(
		ctx,
		service.Page{
			Skip:  1,
			Limit: 1,
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Len(t, page, 1)
	require.Equal(t, "отзыв 2", page[0].Text, "newest first: 3, 2, 1")
	require.Equal(t, int64(2), page[0].UserID)
}
