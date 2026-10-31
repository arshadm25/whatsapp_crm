import { useTranslation } from "react-i18next";
import Icon from "../../components/Icon";
import type { PhoneNumber } from "../../api/types";

// Warnings for connected numbers whose quality is medium or low, or dropped this week.
export default function QualityAlerts({ numbers }: { numbers: PhoneNumber[] }) {
  const { t } = useTranslation();
  const low = numbers.filter((n) => n.quality_dropped || n.quality_rating === "yellow" || n.quality_rating === "red");
  return (
    <>
      {low.map((n) => (
        <div key={n.id} className="banner">
          <Icon name="alert" size="s" />
          <div>
            <b>
              {n.quality_dropped && n.previous_quality_rating
                ? t("numbers.qualityDropped", {
                    number: n.display_phone_number,
                    from: t(`numbers.quality_${n.previous_quality_rating}`),
                    to: t(`numbers.quality_${n.quality_rating}`),
                  })
                : t("numbers.qualityAlert", { number: n.display_phone_number, rating: t(`numbers.quality_${n.quality_rating}`) })}
            </b>
            <span>{t("numbers.qualityAlertHint")}</span>
          </div>
        </div>
      ))}
    </>
  );
}
