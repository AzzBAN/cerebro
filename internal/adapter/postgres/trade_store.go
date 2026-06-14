package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// TradeStore implements port.TradeStore using pgx.
type TradeStore struct {
	pool *pgxpool.Pool
}

// NewTradeStore creates a TradeStore from an existing connection pool.
func NewTradeStore(pool *pgxpool.Pool) *TradeStore {
	return &TradeStore{pool: pool}
}

// SaveIntent persists a new order intent with status 'pending'.
//
// Schema v5 (migration 005_order_brackets) widened this table with the full
// entry contract: order_type, limit_price, stop_price, time_in_force,
// reduce_only, position_side, leverage, scale_out_pct. Older deployments
// pre-migration will reject this insert; callers should run make migrate-up
// before deploying.
func (s *TradeStore) SaveIntent(ctx context.Context, i domain.OrderIntent) error {
	var lev *int
	if i.Leverage > 0 {
		v := i.Leverage
		lev = &v
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO order_intents
			(id, correlation_id, symbol, side, quantity, stop_loss, take_profit_1,
			 strategy, environment, status,
			 order_type, limit_price, stop_price, time_in_force,
			 reduce_only, position_side, leverage, scale_out_pct,
			 created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,
		        $11,$12,$13,$14,$15,$16,$17,$18,
		        NOW(),NOW())
		ON CONFLICT (id) DO NOTHING`,
		i.ID, i.CorrelationID, string(i.Symbol), string(i.Side),
		i.Quantity.String(), nullDecimal(i.StopLoss), nullDecimal(i.TakeProfit1),
		string(i.Strategy), string(i.Environment), string(domain.OrderStatusPending),
		string(i.OrderTypeOrDefault()),
		nullDecimal(i.LimitPrice), nullDecimal(i.StopPrice),
		nullString(string(i.TIF)), i.ReduceOnly,
		nullString(string(i.PositionSide)), lev, i.ScaleOutPct,
	)
	if err != nil {
		return fmt.Errorf("postgres: save intent %s: %w", i.ID, err)
	}
	return nil
}

// UpdateIntentStatus transitions an order to the given status.
func (s *TradeStore) UpdateIntentStatus(ctx context.Context, id string, status domain.OrderStatus, brokerID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE order_intents
		SET status=$2, broker_order_id=$3, updated_at=NOW()
		WHERE id=$1`,
		id, string(status), brokerID,
	)
	if err != nil {
		return fmt.Errorf("postgres: update intent status %s: %w", id, err)
	}
	return nil
}

// SaveTrade persists a filled trade record.
func (s *TradeStore) SaveTrade(ctx context.Context, t domain.Trade) error {
	var pnl *string
	if t.PnL != nil {
		v := t.PnL.String()
		pnl = &v
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trades
			(id, intent_id, correlation_id, symbol, side, quantity,
			 fill_price, fees, pnl, strategy, venue, closed_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NOW())
		ON CONFLICT (id) DO NOTHING`,
		t.ID, t.IntentID, t.CorrelationID, string(t.Symbol), string(t.Side),
		t.Quantity.String(), t.FillPrice.String(), t.Fees.String(),
		pnl, string(t.Strategy), string(t.Venue), t.ClosedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save trade %s: %w", t.ID, err)
	}
	return nil
}

// TradesByWindow returns completed trades within the given UTC time window.
func (s *TradeStore) TradesByWindow(ctx context.Context, from, to time.Time) ([]domain.Trade, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, intent_id, correlation_id, symbol, side, quantity,
		       fill_price, fees, pnl, strategy, venue, closed_at, created_at
		FROM trades
		WHERE created_at >= $1 AND created_at < $2
		ORDER BY created_at ASC`,
		from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: trades by window: %w", err)
	}
	defer rows.Close()

	var out []domain.Trade
	for rows.Next() {
		var t domain.Trade
		var qty, fill, fees, pnlStr string
		var pnlPtr *string

		err := rows.Scan(
			&t.ID, &t.IntentID, &t.CorrelationID,
			&t.Symbol, &t.Side, &qty,
			&fill, &fees, &pnlPtr,
			&t.Strategy, &t.Venue, &t.ClosedAt, &t.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan trade: %w", err)
		}
		t.Quantity, _ = decimal.NewFromString(qty)
		t.FillPrice, _ = decimal.NewFromString(fill)
		t.Fees, _ = decimal.NewFromString(fees)
		if pnlPtr != nil {
			pnlStr = *pnlPtr
			pnl, _ := decimal.NewFromString(pnlStr)
			t.PnL = &pnl
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func nullDecimal(d decimal.Decimal) *string {
	if d.IsZero() {
		return nil
	}
	s := d.String()
	return &s
}

// nullString returns a nil pointer for empty strings so pgx writes NULL
// rather than an empty TEXT. Used for optional enum columns (TIF, PositionSide)
// where an empty zero-value should be stored as NULL.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
