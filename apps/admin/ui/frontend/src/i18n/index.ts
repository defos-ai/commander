import type {} from './types';
import { createInstance } from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from './en';

export { useTranslation } from 'react-i18next';
export const i18n = createInstance();

export async function initializeLocalization() {
  if (i18n.isInitialized) return;
  await i18n.use(initReactI18next).init({
    resources: { en: { commander: en } },
    lng: 'en',
    fallbackLng: 'en',
    supportedLngs: ['en'],
    defaultNS: 'commander',
    initAsync: false,
    enableSelector: true,
    returnNull: false,
    returnEmptyString: false,
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
  });
}
