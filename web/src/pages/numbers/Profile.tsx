import { useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useMe, usePhoneNumbers } from "../../api/hooks";
import NumberProfile from "../NumberProfile";
import NumbersHead from "./NumbersHead";

// Numbers > Business profile: what customers see in WhatsApp, one connected number at a time.
// ?number=<id> picks the number; the first connected one is the default.
export default function Profile() {
  const { t } = useTranslation();
  const q = usePhoneNumbers();
  const role = useMe().data?.tenant?.role;
  const manager = role === "owner" || role === "admin";
  const [params, setParams] = useSearchParams();
  const live = q.data?.filter((n) => n.status === "connected") ?? [];
  const number = live.find((n) => n.id === params.get("number")) ?? live[0];

  return (
    <section>
      <NumbersHead />
      {q.isLoading && <div className="muted">{t("common.loading")}</div>}
      {q.data && !number && <div className="card muted">{t("numbers.profileNone")}</div>}
      {number && (
        <>
          {live.length > 1 && (
            <label className="field profile-pick">
              {t("numbers.profileFor")}
              <select value={number.id} onChange={(e) => setParams({ number: e.target.value }, { replace: true })}>
                {live.map((n) => (
                  <option key={n.id} value={n.id}>
                    {n.display_phone_number}{n.verified_name ? ` · ${n.verified_name}` : ""}
                  </option>
                ))}
              </select>
            </label>
          )}
          <div className="split">
            <div className="stack">
              <NumberProfile key={number.id} number={number} canEdit={manager} />
            </div>
            <aside className="stack">
              <div className="card">
                <h2>{t("numbers.profileAsideTitle")}</h2>
                <p className="muted small">{t("numbers.profileAside")}</p>
                <p className="muted small">{t("numbers.profileNameNote")}</p>
              </div>
            </aside>
          </div>
        </>
      )}
    </section>
  );
}
