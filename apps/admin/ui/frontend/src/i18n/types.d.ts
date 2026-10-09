import 'i18next';
import type en from './locales/en';

declare module 'i18next' {
  interface CustomTypeOptions {
    defaultNS: 'commander';
    resources: { commander: typeof en };
    returnNull: false;
    enableSelector: true;
  }
}
