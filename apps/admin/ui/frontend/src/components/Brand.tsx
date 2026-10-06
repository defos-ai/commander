import { useTranslation } from '../i18n';

export function Brand() {
  const { t } = useTranslation();
  return <span className="brand">{t($ => $.brand)}</span>;
}
