import { Navigate, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useMe } from "../api/hooks";
import { TwoFactor } from "./Settings";

// TwoStepRequired asks a member to turn on two-step verification before using a workspace that
// requires it.
export default function TwoStepRequired() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const me = useMe();

  if (me.isLoading) return <div className="center muted">{t("common.loading")}</div>;
  if (!me.data) return <Navigate to="/login" replace />;
  if (!me.data.two_factor_setup_required) return <Navigate to="/" replace />;

  const logout = async () => {
    await api("POST", "/internal/auth/logout");
    qc.clear();
    navigate("/login");
  };

  return (
    <div className="center" style={{ padding: 16 }}>
      <div style={{ maxWidth: 480, width: "100%" }} className="stack">
        <h1>{t("twoStep.requiredTitle")}</h1>
        <p className="muted">{t("twoStep.requiredLead", { workspace: me.data.tenant?.name ?? "" })}</p>
        <TwoFactor />
        <button type="button" className="link" onClick={logout}>{t("nav.logout")}</button>
      </div>
    </div>
  );
}
