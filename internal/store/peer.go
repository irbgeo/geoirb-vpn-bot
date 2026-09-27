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
	return r.decode(&d)
}

// Save inserts or replaces the peer. Fails if another peer on the same
// server already holds the IP.
func (r *PeerRepo) Save(ctx context.Context, p *service.Peer) error {
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
		p, err := r.decode(&docs[i])
		if err != nil {
			// One unreadable row (edited by hand, restored with another
			// key) must not stop expiry, reminders and new keys for the
			// rest. Its IP stays taken through ServerIPs.
			log.Printf("store: skip peer %s: %v", docs[i].IP, err)
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// checkKey opens the secrets of one stored peer, so a wrong DB_SECRET_KEY
// (e.g. after a restore) stops the bot at startup instead of failing quietly
// on every key.
func (r *PeerRepo) checkKey(ctx context.Context) error {
	var d peer
	err := r.coll.FindOne(ctx, matchAll()).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: read a peer to check the key: %w", err)
	}
	if _, err := r.decode(&d); err != nil {
		return fmt.Errorf("store: DB_SECRET_KEY does not open the stored client keys: %w", err)
	}
	return nil
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

// decode converts a document back, decrypting the secrets.
func (r *PeerRepo) decode(d *peer) (*service.Peer, error) {
	p := d.toService()
	var err error
	if p.PrivateKey, err = r.box.open(
		sealInput{
			Text: d.PrivateKey,
			AAD:  d.PublicKey,
		},
	); err != nil {
		return nil, fmt.Errorf("store: peer %s private key: %w", d.IP, err)
	}
	if p.PSK, err = r.box.open(
		sealInput{
			Text: d.PSK,
			AAD:  d.PublicKey,
		},
	); err != nil {
		return nil, fmt.Errorf("store: peer %s psk: %w", d.IP, err)
	}
	return p, nil
}
