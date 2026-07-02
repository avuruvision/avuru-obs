// Package profilesadapter converts OTLP Profiles (v1development — ALPHA wire
// format) into storage.ProfileSample rows. ALL knowledge of the alpha format
// lives here, per agent_docs/tech_stack.md: the pdata/pprofile dependency is
// pinned to the same collector release line as the profiler distro image, and
// nothing outside this package touches pprofile types.
package profilesadapter

import (
	"fmt"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pprofile"
	"go.opentelemetry.io/collector/pdata/pprofile/pprofileotlp"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ParseProto unmarshals an OTLP/HTTP ExportProfilesServiceRequest body and
// flattens it into samples for the given tenant.
func ParseProto(body []byte, tenant string) ([]storage.ProfileSample, error) {
	req := pprofileotlp.NewExportRequest()
	if err := req.UnmarshalProto(body); err != nil {
		return nil, fmt.Errorf("unmarshaling profiles request: %w", err)
	}
	return Convert(req.Profiles(), tenant), nil
}

// Convert flattens pdata profiles into ProfileSamples: one row per (sample,
// stack), with frames resolved leaf-first through the dictionary.
func Convert(profiles pprofile.Profiles, tenant string) []storage.ProfileSample {
	dic := profiles.Dictionary()
	strTable := dic.StringTable()
	str := func(i int32) string {
		if i < 0 || int(i) >= strTable.Len() {
			return ""
		}
		return strTable.At(int(i))
	}

	var out []storage.ProfileSample
	rps := profiles.ResourceProfiles()
	for i := 0; i < rps.Len(); i++ {
		rp := rps.At(i)
		resAttrs := rp.Resource().Attributes()
		sps := rp.ScopeProfiles()
		for j := 0; j < sps.Len(); j++ {
			ps := sps.At(j).Profiles()
			for k := 0; k < ps.Len(); k++ {
				prof := ps.At(k)
				sampleType := sampleTypeString(prof.SampleType(), str)
				profTime := prof.Time().AsTime()

				samples := prof.Samples()
				for m := 0; m < samples.Len(); m++ {
					sample := samples.At(m)
					frames := stackFrames(dic, sample.StackIndex(), str)
					if len(frames) == 0 {
						continue
					}
					// Sample attributes override resource attributes for
					// attribution (the profiler is host-scoped; container
					// identity rides on the sample).
					attrs := pprofile.FromAttributeIndices(dic.AttributeTable(), sample, dic)

					s := storage.ProfileSample{
						Tenant:     tenant,
						Timestamp:  profTime,
						Service:    serviceName(resAttrs, attrs),
						SampleType: sampleType,
						Frames:     frames,
						Value:      sampleValue(sample),
						Node:       lookupEither(attrs, resAttrs, "k8s.node.name"),
						Pod:        lookupEither(attrs, resAttrs, "k8s.pod.name"),
						Container:  lookupEither(attrs, resAttrs, "k8s.container.name"),
					}
					if ts := sample.TimestampsUnixNano(); ts.Len() > 0 {
						s.Timestamp = pcommon.Timestamp(ts.At(0)).AsTime()
					}
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// stackFrames resolves a stack index to leaf-first frame names: the function
// name of each location's line(s), falling back to the hex address.
func stackFrames(dic pprofile.ProfilesDictionary, stackIdx int32, str func(int32) string) []string {
	stacks := dic.StackTable()
	if stackIdx < 0 || int(stackIdx) >= stacks.Len() {
		return nil
	}
	locTable := dic.LocationTable()
	fnTable := dic.FunctionTable()
	locIdxs := stacks.At(int(stackIdx)).LocationIndices()

	frames := make([]string, 0, locIdxs.Len())
	for i := 0; i < locIdxs.Len(); i++ {
		li := locIdxs.At(i)
		if li < 0 || int(li) >= locTable.Len() {
			continue
		}
		loc := locTable.At(int(li))
		lines := loc.Lines()
		named := false
		for l := 0; l < lines.Len(); l++ {
			fi := lines.At(l).FunctionIndex()
			if fi < 0 || int(fi) >= fnTable.Len() {
				continue
			}
			if name := str(fnTable.At(int(fi)).NameStrindex()); name != "" {
				frames = append(frames, name)
				named = true
			}
		}
		if !named {
			frames = append(frames, fmt.Sprintf("0x%x", loc.Address()))
		}
	}
	return frames
}

// serviceName attributes a sample to a service: explicit service.name first,
// then workload/container identity, then the executable — never empty.
func serviceName(resAttrs, sampleAttrs pcommon.Map) string {
	for _, key := range []string{
		"service.name", "k8s.deployment.name", "k8s.container.name", "process.executable.name",
	} {
		if v := lookupEither(sampleAttrs, resAttrs, key); v != "" {
			return v
		}
	}
	return "unknown"
}

// lookupEither reads a string attribute, preferring the first map.
func lookupEither(first, second pcommon.Map, key string) string {
	if v, ok := first.Get(key); ok {
		return v.AsString()
	}
	if v, ok := second.Get(key); ok {
		return v.AsString()
	}
	return ""
}

// sampleValue sums the sample's values; a sample with only timestamps counts
// one event per timestamp; a bare sample counts once.
func sampleValue(sample pprofile.Sample) uint64 {
	var total int64
	values := sample.Values()
	for i := 0; i < values.Len(); i++ {
		total += values.At(i)
	}
	if total <= 0 {
		if n := sample.TimestampsUnixNano().Len(); n > 0 {
			return uint64(n)
		}
		return 1
	}
	return uint64(total)
}

// sampleTypeString renders a profile's sample_type as "<type>:<unit>".
func sampleTypeString(vt pprofile.ValueType, str func(int32) string) string {
	typ, unit := str(vt.TypeStrindex()), str(vt.UnitStrindex())
	if typ == "" {
		typ = "samples"
	}
	if unit == "" {
		unit = "count"
	}
	return typ + ":" + unit
}
