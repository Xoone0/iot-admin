import { useEffect, useMemo, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import {
  ArrowLeft,
  Boxes,
  Building2,
  CalendarClock,
  Cpu,
  Database,
  FileSearch,
  Gauge,
  HeartPulse,
  Menu,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Router,
  ShieldAlert,
  ShieldCheck,
  Sun,
  Timer,
  TrendingUp,
  TriangleAlert,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import type { MetaPayload } from '../api/types';
import { cn } from '../lib/cn';
import { useMetaState } from '../meta';
import {
  BuildingEnergyMobileNavigation,
  BuildingEnergyNavigation,
  ENERGY_OVERVIEW_PATH,
} from './BuildingEnergyNavigation';
export { ENERGY_OVERVIEW_PATH, ENERGY_STATISTICS_PATH, ENERGY_WORKSPACE_PATH } from './BuildingEnergyNavigation';

const THEME_KEY = 'iot-theme';
const SIDER_KEY = 'iot-energy-sider-collapsed';
type Theme = 'light' | 'dark';

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
}

/** 导航图标按实体/只读页的名字取；未知的名字回落到一个中性圆点，避免缺图标的空位。 */
const NAV_ICONS: Record<string, LucideIcon> = {
  'data-domains': Database,
  'thing-models': Boxes,
  terminals: Cpu,
  gateways: Router,
  'quality-rules': ShieldCheck,
  'snapshot-slots': CalendarClock,
  'snapshot-queries': FileSearch,
  'snapshot-minute': Timer,
  'latest-values': Gauge,
  'exception-events': TriangleAlert,
};

function navIcon(name: string): LucideIcon {
  return NAV_ICONS[name] ?? Cpu;
}

interface NavSection {
  title?: string;
  items: NavItem[];
}

type Navigation = 'technical' | 'building-energy';

export const ENERGY_PREVIEW = import.meta.env.DEV && import.meta.env.VITE_ENERGY_PREVIEW === '1';
export const ENERGY_LIVE = import.meta.env.VITE_ENERGY_LIVE === '1' && !ENERGY_PREVIEW;

/** 菜单仅用于路由展示。身份认证和数据授权由服务端执行。 */
export function navSections(meta: MetaPayload): NavSection[] {
  const entities = meta.entities.map((e) => ({ name: e.name, title: e.title }));
  const pages = meta.readonly ?? [];
  const isSnapshot = (name: string) => name.startsWith('snapshot');

  const sections: NavSection[] = [
    {
      title: '业务',
      items: entities
        .filter((e) => !isSnapshot(e.name))
        .map((e) => ({ to: `/e/${e.name}`, label: e.title, icon: navIcon(e.name) })),
    },
    {
      title: '快照',
      items: [
        ...entities
          .filter((e) => isSnapshot(e.name))
          .map((e) => ({ to: `/e/${e.name}`, label: e.title, icon: navIcon(e.name) })),
        ...pages
          .filter((p) => isSnapshot(p.slot))
          .map((p) => ({ to: `/r/${p.slot}`, label: p.title, icon: navIcon(p.slot) })),
      ],
    },
    {
      title: '分析',
      items: [
        { to: '/a/overview', label: '能耗总览', icon: Gauge },
        { to: '/a/trends', label: '能耗统计与报表', icon: TrendingUp },
        { to: '/a/quality', label: '用能告警与质量', icon: ShieldAlert },
        { to: '/a/health', label: '运行健康', icon: HeartPulse },
      ],
    },
    {
      title: '观测',
      items: [
        ...pages
          .filter((p) => !isSnapshot(p.slot))
          .map((p) => ({ to: `/r/${p.slot}`, label: p.title, icon: navIcon(p.slot) })),
        ...meta.exceptions.map((p) => ({ to: `/x/${p.slot}`, label: p.title, icon: navIcon(p.slot) })),
      ],
    },
  ];
  return sections.filter((s) => s.items.length > 0);
}

/** `/` 重定向目标：导航第一项。 */
export function firstPath(meta: MetaPayload, navigation: Navigation = 'technical'): string {
  if ((ENERGY_PREVIEW || ENERGY_LIVE) && navigation === 'building-energy') return ENERGY_OVERVIEW_PATH;
  return navSections(meta)[0]?.items[0]?.to ?? '';
}

function readTheme(): Theme {
  const saved = localStorage.getItem(THEME_KEY);
  if (saved === 'light' || saved === 'dark') return saved;
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark');
}

/** 首帧前调用，避免暗色用户白闪。 */
export function applyStoredTheme() {
  applyTheme(readTheme());
}

export function AppShell({ navigation = 'technical' }: { navigation?: Navigation }) {
  const { meta } = useMetaState();
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const [navOpen, setNavOpen] = useState(false);
  const [theme, setTheme] = useState<Theme>(readTheme);
  const [siderCollapsed, setSiderCollapsed] = useState<boolean>(() => localStorage.getItem(SIDER_KEY) === '1');
  const energyView = (ENERGY_PREVIEW || ENERGY_LIVE) && navigation === 'building-energy';
  const sections = useMemo(() => (energyView || !meta ? [] : navSections(meta)), [meta, energyView]);

  // 详情页（如 /e/thing-models/1）的父级导航项：顶栏据此渲染"返回"。
  const parent = sections.flatMap((s) => s.items).find((item) => pathname.startsWith(item.to + '/'));

  useEffect(() => applyTheme(theme), [theme]);
  useEffect(() => setNavOpen(false), [pathname]);

  useEffect(() => {
    if (!navOpen) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setNavOpen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [navOpen]);

  const nextTheme: Theme = theme === 'dark' ? 'light' : 'dark';
  const toggleTheme = () => {
    localStorage.setItem(THEME_KEY, nextTheme);
    setTheme(nextTheme);
  };

  const toggleSider = () => {
    setSiderCollapsed((prev) => {
      const next = !prev;
      localStorage.setItem(SIDER_KEY, next ? '1' : '0');
      return next;
    });
  };

  return (
    <div className="min-h-screen bg-canvas text-fg">
      {!energyView && navOpen && (
        <div
          className="fixed inset-0 z-30 bg-fg/40 lg:hidden"
          onClick={() => setNavOpen(false)}
          aria-hidden
        />
      )}

      {energyView ? (
        <aside
          className={cn(
            'fixed inset-y-0 left-0 z-40 hidden flex-col border-r border-line bg-surface transition-[width] duration-150 lg:flex',
            siderCollapsed ? 'w-[56px]' : 'w-[220px]',
          )}
          aria-label="侧边导航"
        >
          <div
            className={cn(
              'flex h-[48px] shrink-0 items-center border-b border-line',
              siderCollapsed ? 'justify-center px-2' : 'gap-2.5 px-4',
            )}
          >
            <span className="flex size-7 shrink-0 items-center justify-center rounded-[4px] bg-primary text-white">
              <Building2 className="size-4" aria-hidden />
            </span>
            {!siderCollapsed && (
              <span className="truncate text-[14px] font-semibold tracking-tight text-fg">
                建筑能耗监测分析
              </span>
            )}
          </div>

          <BuildingEnergyNavigation collapsed={siderCollapsed} />

          {/* Arco Design Pro 侧栏底部的折叠按钮 */}
          <div className="shrink-0 border-t border-line p-2">
            <button
              type="button"
              onClick={toggleSider}
              aria-label={siderCollapsed ? '展开侧边导航' : '收起侧边导航'}
              title={siderCollapsed ? '展开侧边导航' : '收起侧边导航'}
              className={cn(
                'flex h-8 w-full items-center rounded-[4px] text-[12px] text-fg-2 transition-colors hover:bg-fill-2 hover:text-fg',
                siderCollapsed ? 'justify-center px-0' : 'justify-between px-2.5',
              )}
            >
              {!siderCollapsed && <span>收起侧栏</span>}
              {siderCollapsed ? (
                <PanelLeftOpen className="size-4 shrink-0" aria-hidden />
              ) : (
                <PanelLeftClose className="size-4 shrink-0" aria-hidden />
              )}
            </button>
          </div>
        </aside>
      ) : (
        <aside
          className={cn(
            'fixed inset-y-0 left-0 z-40 flex w-[220px] flex-col border-r border-line bg-surface transition-transform duration-200 lg:translate-x-0',
            navOpen ? 'translate-x-0' : 'invisible -translate-x-full lg:visible',
          )}
          aria-label="侧边导航"
        >
          <div className="flex h-[56px] shrink-0 items-center gap-2.5 border-b border-line px-4">
            <span className="flex size-7 items-center justify-center rounded-[4px] bg-primary text-white">
              <Cpu className="size-4" aria-hidden />
            </span>
            <span className="flex flex-col leading-tight">
              <span className="text-[14px] font-semibold tracking-tight">IoT 控制台</span>
              <span className="text-[11px] text-muted">接入 · 快照 · 分析 · 观测</span>
            </span>
          </div>
          <nav className="flex-1 overflow-y-auto px-2 py-2.5" aria-label="主导航">
            {sections.map((section, index) => (
              <div
                key={section.title ?? 'building-energy'}
                className={cn(index > 0 && 'mt-2.5 border-t border-line pt-2.5')}
              >
                {section.title ? (
                  <p className="px-3 pb-1 text-[11px] font-medium tracking-[0.06em] text-muted">{section.title}</p>
                ) : null}
                <ul className="space-y-0.5">
                  {section.items.map((item) => {
                    const Icon = item.icon;
                    return (
                      <li key={item.to}>
                        <NavLink
                          to={item.to}
                          className={({ isActive }) =>
                            cn(
                              'group flex h-9 items-center gap-2.5 rounded-[4px] px-2.5 text-[13px] transition-colors',
                              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40',
                              isActive
                                ? 'bg-primary-soft font-medium text-primary'
                                : 'text-fg-2 hover:bg-fill-2 hover:text-fg',
                            )
                          }
                        >
                          {({ isActive }) => (
                            <>
                              <Icon
                                className={cn('size-4 shrink-0', isActive ? 'text-primary' : 'text-muted group-hover:text-fg')}
                                aria-hidden
                              />
                              <span className="truncate">{item.label}</span>
                            </>
                          )}
                        </NavLink>
                      </li>
                    );
                  })}
                </ul>
              </div>
            ))}
          </nav>
        </aside>
      )}

      <div
        className={cn(
          'transition-[padding-left] duration-150',
          energyView ? (siderCollapsed ? 'lg:pl-[56px]' : 'lg:pl-[220px]') : 'lg:pl-[220px]',
        )}
      >
        <header className="sticky top-0 z-20 flex h-[48px] items-center gap-3 border-b border-line bg-surface px-4 lg:px-5">
          {energyView ? (
            <BuildingEnergyMobileNavigation />
          ) : (
            <button
              type="button"
              className="rounded-[4px] p-1.5 text-muted hover:bg-fill-2 hover:text-fg lg:hidden"
              onClick={() => setNavOpen(true)}
              aria-label="打开导航"
            >
              <Menu className="size-4" aria-hidden />
            </button>
          )}

          {!energyView && parent ? (
            <button
              type="button"
              onClick={() => navigate(parent.to)}
              aria-label={`返回${parent.label}`}
              title={`返回${parent.label}`}
              className="inline-flex h-8 items-center gap-1 rounded-[4px] px-2 text-[13px] text-fg-2 hover:bg-fill-2 hover:text-fg"
            >
              <ArrowLeft className="size-4" aria-hidden />
              返回
            </button>
          ) : null}

          <button
            type="button"
            className="ml-auto inline-flex size-8 items-center justify-center rounded-full border border-line bg-surface text-fg-2 transition-colors hover:bg-fill-2 hover:text-fg"
            onClick={toggleTheme}
            aria-label={`切换到${nextTheme === 'dark' ? '深色' : '浅色'}主题`}
            title={`切换到${nextTheme === 'dark' ? '深色' : '浅色'}主题`}
          >
            {theme === 'dark' ? <Sun className="size-4" aria-hidden /> : <Moon className="size-4" aria-hidden />}
          </button>
        </header>

        <main className={cn('min-h-[calc(100vh-48px)]', energyView ? 'px-4 py-4 lg:px-5 lg:py-4' : 'p-4 lg:p-5')}>
          <div key={energyView ? navigation : pathname} className="page-enter">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
