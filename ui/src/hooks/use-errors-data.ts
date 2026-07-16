"use client";

import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { apiGet, apiPost } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type {
  ErrorEventsResponse,
  ErrorHistogramResponse,
  ErrorIssue,
  ErrorIssueStatus,
  ErrorIssuesResponse,
} from "@/lib/api-types";

export interface IssueFilters {
  status?: string;
  service?: string;
  q?: string;
  sort?: string;
}

export function useErrorIssues(time: TimeParams, filters: IssueFilters) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.errorIssues(project, time, { ...filters }),
    queryFn: () =>
      apiGet<ErrorIssuesResponse>(
        "/api/v1/errors/issues",
        { ...time, ...filters, limit: 200 },
        { project },
      ),
  });
}

export function useErrorIssue(fingerprint: string | null) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.errorIssue(project, fingerprint ?? ""),
    queryFn: () =>
      apiGet<ErrorIssue>(`/api/v1/errors/issues/${fingerprint}`, undefined, { project }),
    enabled: Boolean(fingerprint),
  });
}

export function useErrorIssueEvents(fingerprint: string | null) {
  const { project } = useProject();
  return useInfiniteQuery({
    queryKey: queryKeys.errorIssueEvents(project, fingerprint ?? ""),
    queryFn: ({ pageParam }) =>
      apiGet<ErrorEventsResponse>(
        `/api/v1/errors/issues/${fingerprint}/events`,
        { limit: 50, cursor: pageParam || undefined },
        { project },
      ),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    enabled: Boolean(fingerprint),
  });
}

export function useErrorIssueHistogram(fingerprint: string | null, time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.errorIssueHistogram(project, fingerprint ?? "", time),
    queryFn: () =>
      apiGet<ErrorHistogramResponse>(
        `/api/v1/errors/issues/${fingerprint}/histogram`,
        { ...time, points: 48 },
        { project },
      ),
    enabled: Boolean(fingerprint),
  });
}

export function useSetIssueStatus() {
  const { project } = useProject();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ fingerprint, status }: { fingerprint: string; status: ErrorIssueStatus }) =>
      apiPost<ErrorIssue>(
        `/api/v1/errors/issues/${fingerprint}/status`,
        { status },
        { project },
      ),
    onSuccess: () => {
      // The issue list and detail both change: invalidate every errors key
      // for this project.
      qc.invalidateQueries({ queryKey: [project, "errors"] });
    },
  });
}
