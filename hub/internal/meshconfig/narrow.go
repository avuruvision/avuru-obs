package meshconfig

// Narrow returns the snapshot restricted to the named namespaces: their rows,
// the objects, pods, workloads and services filed under them, and kind counts
// that describe what is left rather than the cluster.
//
// The snapshot itself has no idea of a project — it is what the cluster
// declares, read once and served to every caller. Who may see which part of
// it is the API layer's question, and this is the one operation it needs: a
// view, never a second read. A cluster-scoped object (no namespace) is not
// kept, because nothing in a project's scope claims it.
//
// The read-level facts survive untouched — state, reason, missing kinds,
// truncation, sync times — because they are true of the read, not of the
// view, and a narrowed snapshot that dropped "the pod list was cut" would
// read as a clean bill. A snapshot that was not read (State != OK) comes back
// whole: there is nothing to narrow, and its reason must reach the caller.
//
// The receiver is not mutated: the reader memoises it and serves it to other
// callers with other scopes.
func (s Snapshot) Narrow(keep map[string]bool) Snapshot {
	if s.State != StateOK {
		return s
	}
	out := s
	out.Namespaces = make([]Namespace, 0, len(keep))
	for _, ns := range s.Namespaces {
		if keep[ns.Name] {
			out.Namespaces = append(out.Namespaces, ns)
		}
	}
	out.Objects = make([]Object, 0, len(s.Objects))
	for _, o := range s.Objects {
		if o.Namespace != "" && keep[o.Namespace] {
			out.Objects = append(out.Objects, o)
		}
	}
	out.Pods = make([]Pod, 0, len(s.Pods))
	for _, p := range s.Pods {
		if keep[p.Namespace] {
			out.Pods = append(out.Pods, p)
		}
	}
	out.Workloads = make([]Workload, 0, len(s.Workloads))
	for _, wl := range s.Workloads {
		if keep[wl.Namespace] {
			out.Workloads = append(out.Workloads, wl)
		}
	}
	out.Services = make([]Service, 0, len(s.Services))
	for _, svc := range s.Services {
		if keep[svc.Namespace] {
			out.Services = append(out.Services, svc)
		}
	}
	out.Kinds = recountKinds(s.Kinds, out)
	return out
}

// recountKinds rewrites each kind's Count from the narrowed view, keeping its
// sync stamps and truncation flag: when the cache warmed and whether it was
// cut are facts about the read; how many of its objects this caller may see
// is not.
func recountKinds(kinds []KindSync, view Snapshot) []KindSync {
	if len(kinds) == 0 {
		return nil
	}
	byKind := map[string]int{}
	for _, o := range view.Objects {
		byKind[o.Kind]++
	}
	out := make([]KindSync, 0, len(kinds))
	for _, k := range kinds {
		switch k.Kind {
		case KindNamespace:
			k.Count = len(view.Namespaces)
		case KindPod:
			k.Count = len(view.Pods)
		default:
			k.Count = byKind[k.Kind]
		}
		out = append(out, k)
	}
	return out
}
