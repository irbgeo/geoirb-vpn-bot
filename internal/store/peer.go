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

// peerRepo stores issued VPN keys in MongoDB. The document _id is the
// peer public key.
type peerRepo struct {
	coll *mongo.Collection
	box  *sealer // PrivateKey and PSK are stored encrypted
}

// Get returns the peer by public key, or (nil, nil) if not found.
func (s *peerRepo) Get(ctx context.Context, publicKey string) (*service.Peer, error) {
	var d peer
	err := s.coll.FindOne(ctx, byID(publicKey)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get peer: %w", err)
	}
	return s.decode(&d), nil
}

// Save inserts or replaces the peer. Fails if another peer on the same
// server already holds the IP. A peer without a PSK was loaded without its
// secrets (they could not be read): only its other fields are updated, the
// stored sealed secrets are kept.
func (s *peerRepo) Save(ctx context.Context, p *service.Peer) error {
	if p.PSK == "" {
		_, err := s.coll.UpdateOne(ctx, byID(p.PublicKey), setPeerMeta(peerToStore(p)))
		if err != nil {
			return fmt.Errorf("store: save peer %s: %w", p.IP, err)
		}
		return nil
	}
	d, err := s.encode(p)
	if err != nil {
		return err
	}
	_, err = s.coll.ReplaceOne(ctx, byID(p.PublicKey), d, upsert())
	if err != nil {
		return fmt.Errorf("store: save peer %s: %w", p.IP, err)
	}
	return nil
}

// Delete removes the peer; unknown keys are a no-op.
func (s *peerRepo) Delete(ctx context.Context, publicKey string) error {
	_, err := s.coll.DeleteOne(ctx, byID(publicKey))
	if err != nil {
		return fmt.Errorf("store: delete peer: %w", err)
	}
	return nil
}

// ByUser returns all peers of a Telegram user.
func (s *peerRepo) ByUser(ctx context.Context, userID int64) ([]*service.Peer, error) {
	return s.find(ctx, byUserID(userID))
}

// ServerIPs returns the tunnel IP of every peer on a server, also of rows
// whose secrets can't be read: those IPs must stay taken.
func (s *peerRepo) ServerIPs(ctx context.Context, serverID string) ([]string, error) {
	cur, err := s.coll.Find(ctx, byServerID(serverID), ipOnly())
	if err != nil {
		return nil, fmt.Errorf("store: peer IPs: %w", err)
	}
	var docs []peer
	err = cur.All(ctx, &docs)
	if err != nil {
		return nil, fmt.Errorf("store: decode peer IPs: %w", err)
	}
	ips := make([]string, 0, len(docs))
	for i := range docs {
		ips = append(ips, docs[i].IP)
	}
	return ips, nil
}

// ByServer returns all peers on a server.
func (s *peerRepo) ByServer(ctx context.Context, serverID string) ([]*service.Peer, error) {
	return s.find(ctx, byServerID(serverID))
}

// newPeerRepo builds a peerRepo on coll; box seals the secrets.
func newPeerRepo(
	coll *mongo.Collection,
	box *sealer,
) *peerRepo {
	return &peerRepo{
		coll: coll,
		box:  box,
	}
}

func (s *peerRepo) find(ctx context.Context, filter bson.M) ([]*service.Peer, error) {
	cur, err := s.coll.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("store: find peers: %w", err)
	}
	var docs []peer
	err = cur.All(ctx, &docs)
	if err != nil {
		return nil, fmt.Errorf("store: decode peers: %w", err)
	}
	out := make([]*service.Peer, 0, len(docs))
	for i := range docs {
		out = append(out, s.decode(&docs[i]))
	}
	return out, nil
}

// checkKey opens the secrets of up to checkKeySample stored peers, so a
// wrong DB_SECRET_KEY (e.g. after a restore) stops the bot at startup
// instead of failing quietly on every key. One bad row among good ones is
// fine (it is loaded without secrets); none opening means the key is
// wrong.
func (s *peerRepo) checkKey(ctx context.Context) error {
	cur, err := s.coll.Find(ctx, matchAll(), sample(checkKeySample))
	if err != nil {
		return fmt.Errorf("store: read peers to check the key: %w", err)
	}
	var docs []peer
	err = cur.All(ctx, &docs)
	if err != nil {
		return fmt.Errorf("store: read peers to check the key: %w", err)
	}
	if len(docs) == 0 {
		return nil
	}
	for i := range docs {
		_, err = s.open(&docs[i])
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("store: DB_SECRET_KEY does not open the stored client keys (none of %d checked)", len(docs))
}

// encode converts to a document with the secrets encrypted. The public key
// is the associated data, so a secret only opens on its own peer.
func (s *peerRepo) encode(p *service.Peer) (*peer, error) {
	d := peerToStore(p)
	privateKeySealInput := sealInput{
		Text: p.PrivateKey,
		AAD:  p.PublicKey,
	}
	privateKey, err := s.box.seal(privateKeySealInput)
	if err != nil {
		return nil, err
	}
	pskSealInput := sealInput{
		Text: p.PSK,
		AAD:  p.PublicKey,
	}
	psk, err := s.box.seal(pskSealInput)
	if err != nil {
		return nil, err
	}
	d.PrivateKey = privateKey
	d.PSK = psk
	return d, nil
}

// decode converts a document back, decrypting the secrets. Secrets that
// do not open are left empty (logged): one bad row must not stop expiry,
// reminders and new keys for the rest.
func (s *peerRepo) decode(d *peer) *service.Peer {
	p, err := s.open(d)
	if err != nil {
		log.Printf("store: peer %s secrets unreadable: %v", d.IP, err)
	}
	return p
}

// open decrypts a document's secrets; if they do not open, the error is
// returned with the peer, its secrets empty.
func (s *peerRepo) open(d *peer) (*service.Peer, error) {
	p := d.toService()
	privateKeySealInput := sealInput{
		Text: d.PrivateKey,
		AAD:  d.PublicKey,
	}
	priv, err1 := s.box.open(privateKeySealInput)
	pskSealInput := sealInput{
		Text: d.PSK,
		AAD:  d.PublicKey,
	}
	psk, err2 := s.box.open(pskSealInput)
	err := errors.Join(err1, err2)
	if err != nil {
		p.PrivateKey, p.PSK = "", "" // toService copied the sealed text
		return p, err
	}
	p.PrivateKey, p.PSK = priv, psk
	return p, nil
}
