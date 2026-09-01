package api

import (
	"context"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/ai"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// AIBudgetUsage computes month-to-date AI spend for one tenant: tokens and cost
// per calling service, the estate totals, and how many calls could not be
// priced.
//
// Exported and shared for the same reason green's roll-up is: the alerting tick
// and anything that serves these numbers must fire on ONE implementation of the
// math. Service groups taught this when the alerting evaluator turned out to be
// reading different config than the API served.
//
// Cost is applied HERE, from the operator's declared rates, rather than in SQL —
// the same separation the AI tables already keep. That is what lets a price
// change take effect without a migration, and what keeps the evaluator and the
// screen quoting the same number.
func AIBudgetUsage(ctx context.Context, store storage.Store, cfg ai.Config, tenant string, now time.Time) (ai.BudgetUsage, error) {
	rows, err := store.AISpendByService(ctx, storage.AIQuery{
		Tenant:     tenant,
		Range:      monthToDate(now),
		ExcludeAux: true,
	})
	if err != nil {
		return ai.BudgetUsage{}, err
	}

	u := ai.BudgetUsage{
		TokensByService:        map[string]int64{},
		CostByService:          map[string]float64{},
		UnpricedCallsByService: map[string]uint64{},
		KnownServices:          map[string]bool{},
	}
	for _, r := range rows {
		u.KnownServices[r.Service] = true

		tokens := int64(r.InputTokens + r.OutputTokens)
		u.TokensByService[r.Service] += tokens
		u.EstateTokens += tokens

		// An unpriced model contributes tokens but no money, and is COUNTED so
		// a cost budget can say its figure is a floor. Pricing it at zero and
		// staying quiet is what would let a budget come in under every
		// threshold by being ignorant of half the spend.
		if p, _, ok := cfg.Lookup(r.Model); ok {
			cost := p.Cost(r.InputTokens, r.OutputTokens)
			u.CostByService[r.Service] += cost
			u.EstateCost += cost
		} else {
			u.UnpricedCallsByService[r.Service] += r.Calls
			u.UnpricedEstateCalls += r.Calls
		}
	}
	return u, nil
}
