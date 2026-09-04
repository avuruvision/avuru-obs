package tracestats

import "github.com/avuru/avuru-obs/hub/internal/storage"

type storageSpan = storage.Span

func spans(s ...storage.Span) []storage.Span { return s }
