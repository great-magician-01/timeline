// 全局 UI 原语的 Tailwind 等价组合。
// 仅使用完整静态类名，保证 Tailwind 内容扫描不会遗漏；颜色仍来自 tokens.css。
export const ui = {
  stack: 'flex flex-col gap-[14px]',
  // 信息流与列表主列：随壳宽自适应（宽屏约 3/5 视口），不再固定 620px 上限
  constrain: 'mx-auto w-full',
  // 窄栏：登录、注册、404 与设置等表单页面保持常规阅读宽度
  narrow: 'mx-auto w-full max-w-[620px]',
  card: 'rounded-[14px] border border-solid border-border bg-surface',
  cardPad: 'px-[22px] py-5',
  pageHead: 'm-0',
  pageTitle: 'm-0 text-[16.5px] font-extrabold tracking-[0.01em]',
  pageDesc: 'm-0 mt-[2px] text-[13px] text-ink-3',
  form: 'mt-[18px]',
  muted: 'text-[13px] text-ink-3',
  mono: 'tabular-nums',

  button:
    'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-[9px] border border-solid border-transparent px-5 py-2 text-[14.5px] font-semibold transition-[background,border-color,color] duration-150 ease-[ease] hover:no-underline disabled:cursor-not-allowed disabled:opacity-[0.55]',
  buttonPrimary: 'bg-btn-bg text-btn-fg hover-not-disabled:bg-btn-hover',
  buttonSecondary: 'border-border! bg-surface text-ink hover-not-disabled:bg-hover',
  buttonDanger:
    'border-border! bg-surface text-red hover-not-disabled:border-red! hover-not-disabled:bg-red-soft',
  buttonSmall: 'rounded-[8px]! px-[14px]! py-[5px]! text-[13.5px]!',
  buttonBlock: 'w-full',
  buttonSpinner: 'size-[14px]! border-btn-fg! border-t-transparent! opacity-80',

  field: 'mb-4',
  fieldLabel: 'mb-[6px] block text-[13.5px] font-semibold',
  fieldHint: 'm-0 mt-[6px] text-[12.5px] text-ink-3',
  fieldError: 'm-0 mt-[6px] text-[12.5px] text-red',
  input:
    'w-full rounded-[10px] border border-solid border-border bg-surface px-3 py-2 text-[14.5px]! text-ink transition-[border-color,box-shadow] duration-150 ease-[ease] [font:inherit] placeholder:text-ink-3 hover-not-disabled:border-soft-hover focus:border-primary focus:shadow-[0_0_0_3px_var(--primary-soft)] focus:outline-none disabled:cursor-not-allowed disabled:bg-soft disabled:text-ink-2',
  textarea: 'min-h-24 resize-y leading-[1.7]!',

  alert:
    'm-0 mb-[14px] flex items-start gap-[10px] rounded-[10px] border border-solid border-border bg-surface-subtle px-[14px] py-[10px] text-[13.5px] text-ink last:mb-0',
  alertText: 'min-w-0',
  alertIcon: 'mt-[3px] text-ink-3',
  alertVariants: {
    error: { root: 'border-red! bg-red-soft!', icon: 'text-red!' },
    warning: { root: '', icon: 'text-red!' },
    success: { root: '', icon: 'text-green!' },
    info: { root: 'border-primary! bg-primary-soft!', icon: 'text-primary!' },
  },

  badge:
    'inline-flex items-center gap-1 rounded-[999px] bg-soft px-2 py-1 align-middle text-[11px] leading-none font-semibold text-ink-2',
  badgeAi: 'bg-violet-soft! text-violet!',
  badgeDanger: 'bg-red-soft! text-red!',
  badgeMuted: 'text-ink-3!',

  segment: 'inline-flex gap-[2px] rounded-[10px] bg-soft p-[2px]',
  segmentButton:
    'rounded-[8px] border border-solid border-transparent px-[11px] py-1 text-[12.5px] text-ink-2 transition-[background,color] duration-150 ease-[ease] hover-not-disabled:text-ink',
  segmentButtonActive: 'border-border! bg-surface font-bold text-ink!',

  tabs: 'flex gap-1 border-b border-solid border-border',
  tabLink:
    '-mb-px border-b-2 border-solid border-transparent px-3 pt-2 pb-[9px] text-[14px] font-semibold text-ink-2 hover:text-ink hover:no-underline',
  tabLinkActive: 'border-b-ink! text-ink!',

  spinner:
    'size-4 animate-tl-spin rounded-[50%] border-2 border-solid border-soft-hover border-t-primary motion-reduce:animate-[tl-spin_2.4s_linear_infinite]',
  loadingRow: 'm-0 flex items-center justify-center gap-[10px] px-4 py-6 text-[13.5px] text-ink-3',
  empty: 'px-5 py-9 text-center text-ink-3',
  emptyIcon: 'm-0 mx-auto mb-[10px] text-ink-3',
  emptyStrong: 'mb-1 block text-[14.5px] text-ink',
  emptyText: 'm-0 text-[13px]',

  infoList: 'm-0 grid grid-cols-[96px_minmax(0,1fr)] gap-x-4 gap-y-2 text-[13.5px]',
  infoTerm: 'text-ink-3',
  infoDetails: 'm-0 min-w-0 [word-break:break-word]',

  switch: 'inline-flex items-center gap-[10px]',
  switchInput:
    "relative m-0 h-6 w-10 flex-none cursor-pointer appearance-none rounded-[999px] border border-solid border-border bg-soft-hover transition-[background,border-color] duration-150 ease-[ease] after:absolute after:top-[2px] after:left-[2px] after:size-[18px] after:rounded-[50%] after:border after:border-solid after:border-border after:bg-surface after:transition-[transform] after:duration-150 after:ease-[ease] after:content-[''] checked:border-green checked:bg-green checked:after:[transform:translateX(16px)] disabled:cursor-not-allowed disabled:opacity-[0.55]",
} as const

export type AlertKind = keyof typeof ui.alertVariants
