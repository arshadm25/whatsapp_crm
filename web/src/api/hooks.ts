import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "./client";
import type { ContactSummary, InboxCounts, Me, Media, Member, PhoneNumber, PublicConfig, QuickReply, Tag, Template } from "./types";

export function useMe() {
  return useQuery<Me | null>({
    queryKey: ["me"],
    queryFn: async () => {
      try {
        return await api<Me>("GET", "/internal/auth/me");
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) return null;
        throw e;
      }
    },
    staleTime: 60_000,
  });
}

export function useConfig() {
  return useQuery({
    queryKey: ["config"],
    queryFn: () => api<PublicConfig>("GET", "/internal/config"),
    staleTime: Infinity,
  });
}

export function usePhoneNumbers() {
  return useQuery({
    queryKey: ["phone-numbers"],
    queryFn: async () => (await api<{ data: PhoneNumber[] }>("GET", "/v1/phone-numbers")).data,
  });
}

export function useTemplates(status?: string) {
  return useQuery({
    queryKey: ["templates", status ?? "all"],
    queryFn: async () => {
      const qs = new URLSearchParams({ limit: "100" });
      if (status) qs.set("status", status);
      return (await api<{ data: Template[] }>("GET", `/v1/templates?${qs}`)).data;
    },
  });
}

export function useTags() {
  return useQuery({
    queryKey: ["tags"],
    queryFn: async () => (await api<{ data: Tag[] }>("GET", "/internal/contacts/tags")).data,
  });
}

// Inbox tab counts and the unread badge. The key sits under "conversations" so live events
// refresh it with the list.
export function useInboxCounts() {
  return useQuery({
    queryKey: ["conversations", "counts"],
    queryFn: () => api<InboxCounts>("GET", "/internal/inbox/counts"),
  });
}

export function useContactSummary() {
  return useQuery({
    queryKey: ["contacts", "summary"],
    queryFn: () => api<ContactSummary>("GET", "/internal/contacts/summary"),
  });
}

export function useMembers() {
  return useQuery({
    queryKey: ["members"],
    queryFn: async () => (await api<{ data: Member[] }>("GET", "/internal/inbox/members")).data,
    staleTime: 60_000,
  });
}

export function useQuickReplies() {
  return useQuery({
    queryKey: ["quick-replies"],
    queryFn: async () => (await api<{ data: QuickReply[] }>("GET", "/internal/inbox/quick-replies")).data,
  });
}

// useLiveEvents keeps cached data fresh from the server's event stream: every message,
// conversation or template change refetches the queries that show it.
export function useLiveEvents() {
  const qc = useQueryClient();
  useEffect(() => {
    if (typeof EventSource === "undefined") return;
    const es = new EventSource("/internal/events");
    const onChange = (e: MessageEvent) => {
      const ev = JSON.parse(e.data) as { type: string; id: string; conversation_id?: string };
      if (ev.type === "template") {
        qc.invalidateQueries({ queryKey: ["templates"] });
        return;
      }
      if (ev.type === "notification") {
        qc.invalidateQueries({ queryKey: ["notifications"] });
        return;
      }
      qc.invalidateQueries({ queryKey: ["conversations"] });
      if (ev.conversation_id) {
        qc.invalidateQueries({ queryKey: ["conversation", ev.conversation_id] });
        qc.invalidateQueries({ queryKey: ["conversation-messages", ev.conversation_id] });
      }
      if (ev.type === "message") qc.invalidateQueries({ queryKey: ["message", ev.id] });
    };
    for (const type of ["message", "conversation", "template", "notification"]) es.addEventListener(type, onChange);
    return () => es.close();
  }, [qc]);
}

// A file's details and download link. Links last 15 minutes, so they are refetched after 10.
export function useMedia(id: string | null) {
  return useQuery({
    queryKey: ["media", id],
    queryFn: () => api<Media>("GET", `/v1/media/${id}`),
    enabled: !!id,
    staleTime: 10 * 60_000,
    refetchInterval: 10 * 60_000,
  });
}
