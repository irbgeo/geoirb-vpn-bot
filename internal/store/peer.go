package store

import (
	"context"
	"errors"
	"fmt"
	"log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// checkKeySample: how many peers checkKey tries.
const checkKeySample = 20

// PeerRepo stores issued VPN keys in MongoDB. The document _id is the
// peer public key.
type PeerRepo struct {
	coll *mongo.Collection
	box  *sealer // PrivateKey and PSK are stored encrypted
}

// Get returns the peer by public key, or (nil, nil) if not found.
func (r *PeerRepo) Get(ctx context.Context, publicKey string) (*service.Peer, error) {
	var d peer
	err := r.coll.FindOne(ctx, byID(publicKey)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get peer: %w", err)
	}
	return r.decode(&d), nil
}

// Save inserts or replaces the peer. Fails if another peer on the same
// server already holds the IP. An Unreadable peer has no secrets to write:
// only its other fields are updated, the stored sealed secrets are kept.
func (r *PeerRepo) Save(ctx context.Context, p *service.Peer) error {
	if p.Unreadable {
		if _, err := r.coll.UpdateOne(ctx, byID(p.PublicKey), setPeerMeta(peerToStore(p))); err != nil {
			return fmt.Errorf("store: save peer %s: %w", p.IP, err)
		}
		return nil
	}
	d, err := r.encode(p)
	if err != nil {
		return err
	}
	_, err = r.coll.ReplaceOne(ctx, byID(p.PublicKey), d, upsert())
	if err != nil {
		return fmt.Errorf("store: save peer %s: %w", p.IP, err)
	}
	return nil
}

// Delete removes the peer; unknown keys are a no-op.
func (r *PeerRepo) Delete(ctx context.Context, publicKey string) error {
	if _, err := r.coll.DeleteOne(ctx, byID(publicKey)); err != nil {
		return fmt.Errorf("store: delete peer: %w", err)
	}
	return nil
}

// ByUser returns all peers of a Telegram user.
func (r *PeerRepo) ByUser(ctx context.Context, userID int64) ([]*service.Peer, error) {
	return r.find(ctx, byUserID(userID))
}

// ServerIPs returns the tunnel IP of every peer on a server, also of rows
// whose secrets can't be read: those IPs must stay taken.
func (r *PeerRepo) ServerIPs(ctx context.Context, serverID string) ([]string, error) {
	cur, err := r.coll.Find(ctx, byServerID(serverID), ipOnly())
	if err != nil {
		return nil, fmt.Errorf("store: peer IPs: %w", err)
	}
	var docs []peer
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("store: decode peer IPs: %w", err)
	}
	ips := make([]string, 0, len(docs))
	for i := range docs {
		ips = append(ips, docs[i].IP)
	}
	return ips, nil
}

// ByServer returns all peers on a server.
func (r *PeerRepo) ByServer(ctx context.Context, serverID string) ([]*service.Peer, error) {
	return r.find(ctx, byServerID(serverID))
}

func (r *PeerRepo) find(ctx context.Context, filter bson.M) ([]*service.Peer, error) {
	cur, err := r.coll.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("store: find peers: %w", err)
	}
	var docs []peer
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("store: decode peers: %w", err)
	}
	out := make([]*service.Peer, 0, len(docs))
	for i := range docs {
		out = append(out, r.decode(&docs[i]))
	}
	return out, nil
}

// checkKey opens the secrets of up to checkKeySample stored peers, so a
// wrong DB_SECRET_KEY (e.g. after a restore) stops the bot at startup
// instead of failing quietly on every key. One bad row among good ones is
// fine (it is marked Unreadable at runtime); none opening means the key is
// wrong.
func (r *PeerRepo) checkKey(ctx context.Context) error {
	cur, err := r.coll.Find(ctx, matchAll(), sample(checkKeySample))
	if err != nil {
		return fmt.Errorf("store: read peers to check the key: %w", err)
	}
	var docs []peer
	if err := cur.All(ctx, &docs); err != nil {
		return fmt.Errorf("store: read peers to check the key: %w", err)
	}
	if len(docs) == 0 {
		return nil
	}
	for i := range docs {
		if !r.decode(&docs[i]).Unreadable {
			return nil
		}
	}
	return fmt.Errorf("store: DB_SECRET_KEY does not open the stored client keys (none of %d checked)", len(docs))
}

// encode converts to a document with the secrets encrypted. The public key
// is the associated data, so a secret only opens on its own peer.
func (r *PeerRepo) encode(p *service.Peer) (*peer, error) {
	d := peerToStore(p)
	var err error
	if d.PrivateKey, err = r.box.seal(
		sealInput{
			Text: p.PrivateKey,
			AAD:  p.PublicKey,
		},
	); err != nil {
		return nil, err
	}
	if d.PSK, err = r.box.seal(
		sealInput{
			Text: p.PSK,
			AAD:  p.PublicKey,
		},
	); err != nil {
		return nil, err
	}
	return d, nil
}

// decode converts a document back, decrypting the secrets. A secret
// that does not open leaves the peer Unreadable with empty secrets
// (logged): one bad row must not stop expiry, reminders and new keys for
// the rest.
func (r *PeerRepo) decode(d *peer) *service.Peer {
	p := d.toService()
	priv, err1 := r.box.open(
		sealInput{
			Text: d.PrivateKey,
			AAD:  d.PublicKey,
		},
	)
	psk, err2 := r.box.open(
		sealInput{
			Text: d.PSK,
			AAD:  d.PublicKey,
		},
	)
	if err := errors.Join(err1, err2); err != nil {
		log.Printf("store: peer %s secrets unreadable: %v", d.IP, err)
		p.Unreadable = true
		p.PrivateKey, p.PSK = "", "" // toService copied the sealed text
		return p
	}
	p.PrivateKey, p.PSK = priv, psk
	return p
}
