import { useTranslation } from "react-i18next";

export default function ComingSoon({ section }: { section: string }) {
  const { t } = useTranslation();
  return (
    <section>
      <h1>{t(`nav.${section}`)}</h1>
      <div className="card muted">{t("common.comingSoonBody")}</div>
    </section>
  );
}
