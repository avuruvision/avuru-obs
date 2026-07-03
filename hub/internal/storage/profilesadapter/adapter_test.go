package profilesadapter

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pprofile"
	"go.opentelemetry.io/collector/pdata/pprofile/pprofileotlp"
)

// buildProfiles constructs a minimal valid OTLP profile: one resource
// (service.name=checkout on node-a), one profile with sample_type
// samples:count, one sample of value 5 over the stack [handler <- main]
// (leaf-first: handler at index 0).
func buildProfiles(t *testing.T, now time.Time) pprofile.Profiles {
	t.Helper()
	profiles := pprofile.NewProfiles()
	dic := profiles.Dictionary()

	str := func(s string) int32 {
		i, err := pprofile.SetString(dic.StringTable(), s)
		if err != nil {
			t.Fatalf("SetString(%q): %v", s, err)
		}
		return i
	}
	str("") // spec: string_table[0] MUST be ""
	fn := func(name string) int32 {
		f := pprofile.NewFunction()
		f.SetNameStrindex(str(name))
		i, err := pprofile.SetFunction(dic.FunctionTable(), f)
		if err != nil {
			t.Fatalf("SetFunction(%q): %v", name, err)
		}
		return i
	}
	loc := func(fnIdx int32) int32 {
		l := pprofile.NewLocation()
		l.Lines().AppendEmpty().SetFunctionIndex(fnIdx)
		i, err := pprofile.SetLocation(dic.LocationTable(), l)
		if err != nil {
			t.Fatalf("SetLocation: %v", err)
		}
		return i
	}

	handlerLoc := loc(fn("handler"))
	mainLoc := loc(fn("main"))
	stack := pprofile.NewStack()
	stack.LocationIndices().Append(handlerLoc, mainLoc) // leaf first
	stackIdx, err := pprofile.SetStack(dic.StackTable(), stack)
	if err != nil {
		t.Fatalf("SetStack: %v", err)
	}

	rp := profiles.ResourceProfiles().AppendEmpty()
	rp.Resource().Attributes().PutStr("service.name", "checkout")
	rp.Resource().Attributes().PutStr("k8s.node.name", "node-a")

	prof := rp.ScopeProfiles().AppendEmpty().Profiles().AppendEmpty()
	prof.SetTime(pcommon.NewTimestampFromTime(now))
	prof.SampleType().SetTypeStrindex(str("samples"))
	prof.SampleType().SetUnitStrindex(str("count"))

	sample := prof.Samples().AppendEmpty()
	sample.SetStackIndex(stackIdx)
	sample.Values().Append(5)
	return profiles
}

func TestConvert(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	samples := Convert(buildProfiles(t, now), "default")

	if len(samples) != 1 {
		t.Fatalf("got %d samples, want 1 (%+v)", len(samples), samples)
	}
	s := samples[0]
	if s.Service != "checkout" || s.Node != "node-a" || s.Tenant != "default" {
		t.Errorf("attribution wrong: %+v", s)
	}
	if s.SampleType != "samples:count" || s.Value != 5 {
		t.Errorf("type/value wrong: %+v", s)
	}
	if len(s.Frames) != 2 || s.Frames[0] != "handler" || s.Frames[1] != "main" {
		t.Errorf("frames wrong (want leaf-first [handler main]): %v", s.Frames)
	}
	if !s.Timestamp.Equal(now) {
		t.Errorf("timestamp = %v, want %v", s.Timestamp, now)
	}
}

func TestConvertFallbackAttribution(t *testing.T) {
	profiles := pprofile.NewProfiles()
	dic := profiles.Dictionary()
	// Spec: string_table[0] MUST be "" — unset strindexes resolve to it.
	_, _ = pprofile.SetString(dic.StringTable(), "")
	nameIdx, _ := pprofile.SetString(dic.StringTable(), "proc")
	f := pprofile.NewFunction()
	f.SetNameStrindex(nameIdx)
	fnIdx, _ := pprofile.SetFunction(dic.FunctionTable(), f)
	l := pprofile.NewLocation()
	l.Lines().AppendEmpty().SetFunctionIndex(fnIdx)
	locIdx, _ := pprofile.SetLocation(dic.LocationTable(), l)
	st := pprofile.NewStack()
	st.LocationIndices().Append(locIdx)
	stIdx, _ := pprofile.SetStack(dic.StackTable(), st)

	// No service.name anywhere; executable name on the resource.
	rp := profiles.ResourceProfiles().AppendEmpty()
	rp.Resource().Attributes().PutStr("process.executable.name", "nginx")
	prof := rp.ScopeProfiles().AppendEmpty().Profiles().AppendEmpty()
	sample := prof.Samples().AppendEmpty()
	sample.SetStackIndex(stIdx)
	// No values, two timestamps -> value 2, timestamp = first event.
	eventNs := uint64(time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC).UnixNano())
	sample.TimestampsUnixNano().Append(eventNs, eventNs+1e9)

	samples := Convert(profiles, "default")
	if len(samples) != 1 {
		t.Fatalf("got %d samples, want 1", len(samples))
	}
	s := samples[0]
	if s.Service != "nginx" {
		t.Errorf("fallback attribution wrong: %q", s.Service)
	}
	if s.Value != 2 {
		t.Errorf("timestamp-count value wrong: %d", s.Value)
	}
	if s.SampleType != "samples:count" {
		t.Errorf("default sample type wrong: %q", s.SampleType)
	}
	if s.Timestamp.UnixNano() != int64(eventNs) {
		t.Errorf("timestamp not taken from first event: %v", s.Timestamp)
	}
}

func TestParseProtoRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	req := pprofileotlp.NewExportRequestFromProfiles(buildProfiles(t, now))
	body, err := req.MarshalProto()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	samples, err := ParseProto(body, "default")
	if err != nil {
		t.Fatalf("ParseProto: %v", err)
	}
	if len(samples) != 1 || samples[0].Service != "checkout" {
		t.Fatalf("round trip wrong: %+v", samples)
	}
	if _, err := ParseProto([]byte("not-proto!!!"), "default"); err == nil {
		t.Error("garbage body must fail")
	}
}
