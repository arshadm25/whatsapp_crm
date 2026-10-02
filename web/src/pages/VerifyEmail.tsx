import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";

export default function VerifyEmail() {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const qc = useQueryClient();
  const [state, setState] = useState<"pending" | "done" | string>("pending");

  useEffect(() => {
    api("POST", "/internal/auth/verify-email", { token: params.get("token") ?? "" })
      .then(() => {
        setState("done");
        void qc.invalidateQueries({ queryKey: ["me"] });
      })
      .catch((e) => setState(e instanceof ApiError ? e.message : t("common.error")));
  }, [params, qc, t]);

  return (
    <div className="auth-page">
      <main className="auth-main">
        <div className="card auth-card">
          <div className="brand">{t("app.name")}</div>
          {state === "pending" && <p>{t("auth.verifying")}</p>}
          {state === "done" && <p>{t("auth.verified")}</p>}
          {state !== "pending" && state !== "done" && <p className="error">{state}</p>}
          <Link className="button primary" to="/">{t("auth.continue")}</Link>
        </div>
      </main>
    </div>
  );
}
