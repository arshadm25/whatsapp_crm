import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "./client";
import type { Me, PhoneNumber, PublicConfig, Template } from "./types";

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
