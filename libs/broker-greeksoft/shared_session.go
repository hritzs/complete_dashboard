package greeksoft

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"

	broker "trading-platform/libs/go-broker"
)

// LoginShared ensures c.Session is populated, reusing a recently-saved
// session from the broker_sessions table when one exists and is fresh
// enough, instead of unconditionally performing a fresh PerformFullLogin.
//
// This exists because GreekSoft appears to allow only one valid session
// per account: reconciler, greeksoft-feed-bridge, and execution-gateway
// each independently calling PerformFullLogin for the same account was
// confirmed (via a live incident, see
// docs/greeksoft-integration-architecture.md) to invalidate each other's
// already-open Iris/Apollo websocket sessions -- the server closes the
// connection gracefully ("close 1000 (normal)") right after a competing
// login. Sharing one login across processes, via the same Postgres both
// warm-path services already depend on, removes that self-inflicted
// collision. It does not (and cannot) prevent a session being invalidated
// by something entirely outside this platform, e.g. a manual GreekSoft
// terminal login.
//
// db may be nil (e.g. a caller without a Postgres connection configured);
// LoginShared then always performs a fresh login, same as calling
// PerformFullLogin directly. Any failure reading or writing the shared
// session is logged and treated as non-fatal -- this is a performance/
// coordination optimization, not a correctness dependency, so a DB hiccup
// should never be the reason a process can't log in to GreekSoft at all.
func (c *Client) LoginShared(ctx context.Context, db *sql.DB, accCfg *broker.AccountConfig, maxAge time.Duration) (*broker.SessionDetails, error) {
	if db != nil {
		if session, ok := loadSharedSession(ctx, db, accCfg.ClientID, maxAge); ok {
			c.Session = session
			log.Printf("[GREEKSOFT] reusing shared session for account=%s (avoids a fresh login that would kick other processes' connections)", accCfg.ClientID)
			return session, nil
		}
	}

	session, err := c.PerformFullLogin(ctx, accCfg)
	if err != nil {
		return nil, err
	}

	if db != nil {
		saveSharedSession(ctx, db, accCfg.ClientID, session)
	}

	return session, nil
}

// InvalidateShared removes the shared session record for accountID, e.g.
// after this process detects its session was rejected -- so the *next*
// caller (this process on reconnect, or another) is forced to perform a
// fresh login rather than reusing the same now-dead session repeatedly.
func InvalidateShared(ctx context.Context, db *sql.DB, accountID string) {
	if db == nil {
		return
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE broker_sessions SET session_state = 'INVALIDATED', updated_at = NOW()
		WHERE broker = 'greeksoft' AND user_id = $1
	`, accountID); err != nil {
		log.Printf("[GREEKSOFT] invalidate shared session for account=%s failed (non-fatal): %v", accountID, err)
	}
}

func loadSharedSession(ctx context.Context, db *sql.DB, accountID string, maxAge time.Duration) (*broker.SessionDetails, bool) {
	var (
		sessionToken  string
		sessionState  sql.NullString
		brokerSpecRaw []byte
		updatedAt     time.Time
	)
	err := db.QueryRowContext(ctx, `
		SELECT session_token, session_state, broker_specific, updated_at
		FROM broker_sessions
		WHERE broker = 'greeksoft' AND user_id = $1
	`, accountID).Scan(&sessionToken, &sessionState, &brokerSpecRaw, &updatedAt)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[GREEKSOFT] load shared session for account=%s failed (falling back to fresh login): %v", accountID, err)
		}
		return nil, false
	}

	if sessionState.Valid && sessionState.String == "INVALIDATED" {
		return nil, false
	}
	if sessionToken == "" || time.Since(updatedAt) > maxAge {
		return nil, false
	}

	var brokerSpecific map[string]interface{}
	if len(brokerSpecRaw) > 0 {
		if err := json.Unmarshal(brokerSpecRaw, &brokerSpecific); err != nil {
			log.Printf("[GREEKSOFT] decode shared session broker_specific for account=%s failed (falling back to fresh login): %v", accountID, err)
			return nil, false
		}
	}

	return &broker.SessionDetails{
		UserID:         accountID,
		AuthToken:      sessionToken,
		IsLoggedIn:     true,
		BrokerSpecific: brokerSpecific,
	}, true
}

func saveSharedSession(ctx context.Context, db *sql.DB, accountID string, session *broker.SessionDetails) {
	if session == nil {
		return
	}
	brokerSpecRaw, err := json.Marshal(session.BrokerSpecific)
	if err != nil {
		log.Printf("[GREEKSOFT] encode shared session for account=%s failed (non-fatal): %v", accountID, err)
		return
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO broker_sessions (broker, user_id, session_token, session_state, broker_specific, created_at, updated_at)
		VALUES ('greeksoft', $1, $2, 'LOGGED_IN', $3, NOW(), NOW())
		ON CONFLICT (broker, user_id) DO UPDATE
		SET session_token = EXCLUDED.session_token,
		    session_state = EXCLUDED.session_state,
		    broker_specific = EXCLUDED.broker_specific,
		    updated_at = NOW()
	`, accountID, session.AuthToken, brokerSpecRaw)
	if err != nil {
		log.Printf("[GREEKSOFT] save shared session for account=%s failed (non-fatal): %v", accountID, err)
	}
}
