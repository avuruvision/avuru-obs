"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { TagsResponse } from "@/lib/api-types";

// Business tags seen in the window, for filter discovery. Nothing is mapped
// until an operator maps it, so an empty list is the normal answer and callers
// render no tag controls at all rather than an empty one.
export function useTags(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.tags(project, time),
    queryFn: () => apiGet<TagsResponse>("/api/v1/tags", { ...time }, { project }),
    select: (data) => data.tags,
  });
}
