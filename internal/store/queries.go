package store

import (
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// This file builds every MongoDB query the repositories run: filters,
// sorts, options and indexes. Repositories call these by name and hold no
// bson literals themselves.

// --- filters ---

func matchAll() bson.M {
	return bson.M{}
}

func byID(id any) bson.M {
	return bson.M{
		"_id": id,
	}
}

func byRole(r service.Role) bson.M {
	return bson.M{
		"role": string(r),
	}
}

func byUserID(userID int64) bson.M {
	return bson.M{
		"user_id": userID,
	}
}

func byServerID(serverID string) bson.M {
	return bson.M{
		"server_id": serverID,
	}
}

func createdSince(t time.Time) bson.M {
	return bson.M{
		"created_at": bson.M{
			"$gte": t,
		},
	}
}

// --- updates ---

// registerUpdate sets the username; everything else only on insert.
func registerUpdate(u *user) bson.M {
	return bson.M{
		"$set": bson.M{
			"username": u.Username,
		},
		"$setOnInsert": bson.M{
			"role":       u.Role,
			"trial_used": u.TrialUsed,
			"created_at": u.CreatedAt,
		},
	}
}

func incKeysCount(delta int) bson.M {
	return bson.M{
		"$inc": bson.M{
			"keys_count": delta,
		},
	}
}

func setKeysCount(n int) bson.M {
	return bson.M{
		"$set": bson.M{
			"keys_count": n,
		},
	}
}

// countedExcept matches users with a non-zero keys count whose ID is not in ids.
func countedExcept(ids []int64) bson.M {
	return bson.M{
		"_id": bson.M{
			"$nin": ids,
		},
		"keys_count": bson.M{
			"$ne": 0,
		},
	}
}

func markApplied(peerKey string) bson.M {
	return bson.M{
		"$set": bson.M{
			"applied":  true,
			"peer_key": peerKey,
		},
	}
}

func markRefunded(t time.Time) bson.M {
	return bson.M{
		"$set": bson.M{
			"refunded_at": t,
		},
	}
}

// setPeerMeta updates every field of a peer except its sealed secrets.
func setPeerMeta(d *peer) bson.M {
	return bson.M{
		"$set": bson.M{
			"server_id":   d.ServerID,
			"user_id":     d.UserID,
			"name":        d.Name,
			"ip":          d.IP,
			"enabled":     d.Enabled,
			"expires_at":  d.ExpiresAt,
			"reminded_3d": d.Reminded3d,
			"reminded_1d": d.Reminded1d,
			"blocked":     d.Blocked,
			"created_at":  d.CreatedAt,
		},
	}
}

// sample limits a find to n documents.
func sample(n int64) *options.FindOptions {
	return options.Find().SetLimit(n)
}

func setTrialUsed() bson.M {
	return bson.M{
		"$set": bson.M{
			"trial_used": true,
		},
	}
}

// ipOnly reads just the tunnel IP of peers (no secrets to decrypt).
func ipOnly() *options.FindOptions {
	return options.Find().SetProjection(
		bson.M{
			"ip": 1,
		},
	)
}

// --- options ---

func upsert() *options.ReplaceOptions {
	return options.Replace().SetUpsert(true)
}

func upsertReturnAfter() *options.FindOneAndUpdateOptions {
	return options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
}

func newestFirst() *options.FindOptions {
	return options.Find().SetSort(
		bson.D{
			{Key: "created_at", Value: -1},
		},
	)
}

// usersByRolePage is one page of users: plain users (and those with no
// role) first, then unlimited, then admins; newest first inside a role.
func usersByRolePage(p service.Page) mongo.Pipeline {
	return mongo.Pipeline{
		{
			{Key: "$addFields", Value: bson.D{
				{Key: "_role_rank", Value: roleRank()},
			}},
		},
		{
			{Key: "$sort", Value: bson.D{
				{Key: "_role_rank", Value: 1},
				{Key: "created_at", Value: -1},
				{Key: "_id", Value: 1},
			}},
		},
		{
			{Key: "$skip", Value: p.Skip},
		},
		{
			{Key: "$limit", Value: p.Limit},
		},
	}
}

// roleRank: unlimited 1, admin 2, anything else (user, no role) 0.
func roleRank() bson.M {
	return bson.M{
		"$switch": bson.M{
			"branches": bson.A{
				bson.M{
					"case": bson.M{
						"$eq": bson.A{
							"$role",
							string(service.RoleUnlimited),
						},
					},
					"then": 1,
				},
				bson.M{
					"case": bson.M{
						"$eq": bson.A{
							"$role",
							string(service.RoleAdmin),
						},
					},
					"then": 2,
				},
			},
			"default": 0,
		},
	}
}

// --- indexes ---

// peerServerIPIndex: one IP per server, so two peers never share an address.
func peerServerIPIndex() mongo.IndexModel {
	return mongo.IndexModel{
		Keys: bson.D{
			{Key: "server_id", Value: 1},
			{Key: "ip", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	}
}
