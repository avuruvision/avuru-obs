package clickhouse

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// stackHash fingerprints a leaf-first frame list (FNV-64a, NUL-separated).
// Collisions across distinct stacks are vanishingly rare and only blur one
// flame-graph branch — acceptable for the dedup key.
func stackHash(frames []string) uint64 {
	h := fnv.New64a()
	for _, f := range frames {
		_, _ = h.Write([]byte(f))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// WriteProfileSamples persists samples with Coroot-style stack dedup: unique
// stacks upsert into profiling_stacks (ReplacingMergeTree keyed by hash),
// samples reference them by hash only.
func (s *Store) WriteProfileSamples(ctx context.Context, samples []storage.ProfileSample) error {
	if len(samples) == 0 {
		return nil
	}

	type stackRow struct {
		tenant string
		frames []string
	}
	stacks := map[uint64]stackRow{}
	hashes := make([]uint64, len(samples))
	for i, sm := range samples {
		h := stackHash(sm.Frames)
		hashes[i] = h
		if _, ok := stacks[h]; !ok {
			stacks[h] = stackRow{tenant: sm.Tenant, frames: sm.Frames}
		}
	}

	stackBatch, err := s.conn.PrepareBatch(ctx, "INSERT INTO profiling_stacks (Tenant, StackHash, Frames)")
	if err != nil {
		return fmt.Errorf("preparing stacks batch: %w", err)
	}
	for h, row := range stacks {
		if err := stackBatch.Append(row.tenant, h, row.frames); err != nil {
			return fmt.Errorf("appending stack: %w", err)
		}
	}
	if err := stackBatch.Send(); err != nil {
		return fmt.Errorf("writing stacks: %w", err)
	}

	sampleBatch, err := s.conn.PrepareBatch(ctx, `INSERT INTO profiling_samples
		(Timestamp, Tenant, ServiceName, SampleType, StackHash, Value, NodeName, PodName, ContainerName)`)
	if err != nil {
		return fmt.Errorf("preparing samples batch: %w", err)
	}
	for i, sm := range samples {
		err := sampleBatch.Append(
			sm.Timestamp, sm.Tenant, sm.Service, sm.SampleType,
			hashes[i], sm.Value, sm.Node, sm.Pod, sm.Container,
		)
		if err != nil {
			return fmt.Errorf("appending sample: %w", err)
		}
	}
	if err := sampleBatch.Send(); err != nil {
		return fmt.Errorf("writing samples: %w", err)
	}
	return nil
}
