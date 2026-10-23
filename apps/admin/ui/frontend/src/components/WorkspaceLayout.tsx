import { useId } from 'react';
import type { ReactNode } from 'react';
import { ArrowLeft, Bot, ClipboardList, Compass, LayoutDashboard, Menu, Server, Settings2, Workflow, X } from 'lucide-react';
import { useTranslation } from '../i18n';
import { useSidebar } from '../hooks/useSidebar';
import { Brand } from './Brand';
import type { WorkspaceSection } from '../navigation/routes';

type WorkspaceLayoutProps = {
  connection: { id: string; name: string; url: string };
  section: WorkspaceSection;
  sections?: WorkspaceSection[];
  onNavigate: (section: WorkspaceSection) => void;
  onBack: () => void;
  children: ReactNode;
};

export function WorkspaceLayout({ connection, section, sections, onNavigate, onBack, children }: WorkspaceLayoutProps) {
  const { t } = useTranslation();
  const sidebar = useSidebar();
  const identity = useId();
  const sidebarId = `${identity}-sidebar`;
  const contentId = `${identity}-content`;
  const activeSection = section === 'secrets' ? 'agents' : section === 'create-mission' ? 'missions' : section;
  const navigation = [
    { section: 'overview' as const, label: t($ => $.shell.nav.overview), Icon: LayoutDashboard },
    { section: 'operations' as const, label: t($ => $.shell.nav.operations), Icon: Workflow },
    { section: 'missions' as const, label: t($ => $.shell.nav.missions), Icon: Compass },
    { section: 'controllers' as const, label: t($ => $.shell.nav.controllers), Icon: Server },
    { section: 'agents' as const, label: t($ => $.shell.nav.agents), Icon: Bot },
    { section: 'actions' as const, label: t($ => $.shell.nav.actions), Icon: ClipboardList },
  ];

  return <div className="platform-layout">
    <a href={`#${contentId}`} className="skip-link" inert={sidebar.modal} onClick={event => {
      event.preventDefault();
      document.getElementById(contentId)?.focus();
    }}>{t($ => $.shell.skipToContent)}</a>
    {sidebar.mobile && <button type="button" ref={sidebar.triggerRef} className="mobile-sidebar-toggle"
      aria-controls={sidebarId} aria-expanded={sidebar.modal} aria-label={t($ => $.shell.openMenu)}
      hidden={sidebar.modal} onClick={() => sidebar.setOpen(true)}>
      <Menu size={19} aria-hidden="true" />
    </button>}
    {sidebar.modal && <div className="sidebar-backdrop" aria-hidden="true" onClick={sidebar.close} />}

    <aside id={sidebarId} ref={sidebar.panelRef} className="workspace-sidebar" tabIndex={-1}
      aria-label={t($ => $.shell.navigation)} hidden={!sidebar.visible}
      role={sidebar.modal ? 'dialog' : undefined} aria-modal={sidebar.modal || undefined}>
      <div className="sidebar-brand">
        <Brand />
        {sidebar.mobile && <button type="button" className="sidebar-close" aria-label={t($ => $.shell.closeMenu)} onClick={sidebar.close}>
          <X size={16} aria-hidden="true" />
        </button>}
      </div>
      <nav aria-label={t($ => $.shell.navigation)}>
        <div className="sidebar-primary">
          {navigation.filter(item => !sections || sections.includes(item.section)).map(({ section: destination, label, Icon }) => <button key={destination} type="button" className="sidebar-command"
            aria-current={activeSection === destination ? 'page' : undefined} onClick={() => { sidebar.close(); onNavigate(destination); }}>
            <Icon size={15} strokeWidth={1.4} aria-hidden="true" />
            <span>{label}</span>
            {activeSection === destination && <span className="nav-marker" aria-hidden="true" />}
          </button>)}
        </div>
        <div className="sidebar-footer">
          <div className="sidebar-connection">
            <span className="eyebrow">{t($ => $.shell.currentConnection)}</span>
            <span className="sidebar-connection-name">{connection.name}</span>
            <span className="sidebar-connection-url">{connection.url}</span>
          </div>
          <div className="sidebar-settings">
            {(!sections || sections.includes('settings')) && <button type="button" className="sidebar-command"
              aria-current={section === 'settings' ? 'page' : undefined} onClick={() => { sidebar.close(); onNavigate('settings'); }}>
              <Settings2 size={15} strokeWidth={1.4} aria-hidden="true" />
              <span>{t($ => $.shell.nav.settings)}</span>
              {section === 'settings' && <span className="nav-marker" aria-hidden="true" />}
            </button>}
            <button type="button" className="sidebar-command" onClick={() => { sidebar.close(); onBack(); }}>
              <ArrowLeft size={15} strokeWidth={1.4} aria-hidden="true" /><span>{t($ => $.shell.allConnections)}</span>
            </button>
          </div>
        </div>
      </nav>
    </aside>

    <main id={contentId} className="workspace-main" tabIndex={-1} inert={sidebar.modal}>
      <div className="workspace-content">
        {children}
      </div>
    </main>
  </div>;
}
