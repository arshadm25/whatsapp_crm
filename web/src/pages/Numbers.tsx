import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { usePhoneNumbers } from "../api/hooks";

export default function Numbers() {
  const { t } = useTranslation();
  const q = usePhoneNumbers();

  return (
    <section>
      <div className="page-head">
        <h1>{t("numbers.title")}</h1>
        <Link className="button primary" to="/numbers/connect">{t("numbers.connect")}</Link>
      </div>
      {q.isLoading && <div className="muted">{t("common.loading")}</div>}
      {q.data?.length === 0 && <div className="card muted">{t("numbers.empty")}</div>}
      {!!q.data?.length && (
        <div className="card table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("numbers.number")}</th>
                <th>{t("numbers.name")}</th>
                <th>{t("numbers.quality")}</th>
                <th>{t("numbers.limit")}</th>
                <th>{t("numbers.status")}</th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((n) => (
                <tr key={n.id}>
                  <td>
                    {n.display_phone_number}
                    {n.is_coexistence && <div className="muted small">{t("numbers.coexistence")}</div>}
                  </td>
                  <td>{n.verified_name ?? "—"}</td>
                  <td><span className={`pill q-${n.quality_rating}`}>{t(`numbers.quality_${n.quality_rating}`)}</span></td>
                  <td>{n.messaging_limit_tier?.replace("TIER_", "") ?? "—"}</td>
                  <td><span className={`pill s-${n.status}`}>{t(`numbers.status_${n.status}`)}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
