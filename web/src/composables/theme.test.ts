// 主题注册表与运行期切换：初始值来自 data-theme 属性，非法值回退浅色
import { afterEach, describe, expect, it, vi } from 'vitest'

interface StubbedDom {
  documentStub: {
    documentElement: { getAttribute: ReturnType<typeof vi.fn>; setAttribute: ReturnType<typeof vi.fn> }
    createElement: () => Record<string, never>
  }
  localStorageStub: { getItem: ReturnType<typeof vi.fn>; setItem: ReturnType<typeof vi.fn> }
}

function stubDom(initialTheme: string | null, throwOnSet = false): StubbedDom {
  const store = new Map<string, string>()
  const documentStub = {
    documentElement: {
      getAttribute: vi.fn(() => initialTheme),
      setAttribute: vi.fn(),
    },
    // vue 的 runtime-dom 在模块初始化时会 document.createElement('template')，
    // node 环境下补一个最小实现避免误入 DOM 缺失分支。
    createElement: () => ({}),
  }
  const localStorageStub = {
    getItem: vi.fn((key: string) => store.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => {
      if (throwOnSet) throw new Error('quota exceeded')
      store.set(key, value)
    }),
  }
  vi.stubGlobal('document', documentStub)
  vi.stubGlobal('localStorage', localStorageStub)
  return { documentStub, localStorageStub }
}

async function loadTheme(initialTheme: string | null, throwOnSet = false) {
  vi.resetModules()
  const stubs = stubDom(initialTheme, throwOnSet)
  const mod = await import('./theme')
  return { mod, ...stubs }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('theme 初始值', () => {
  it('从 data-theme 属性读取合法主题', async () => {
    const { mod } = await loadTheme('eva-01')
    expect(mod.useTheme().theme.value).toBe('eva-01')
  })

  it('非法或缺失的属性值回退浅色', async () => {
    const invalid = await loadTheme('not-a-theme')
    expect(invalid.mod.useTheme().theme.value).toBe('light')

    const missing = await loadTheme(null)
    expect(missing.mod.useTheme().theme.value).toBe('light')
  })
})

describe('theme 切换', () => {
  it('setTheme 同步更新 ref、DOM 属性与 localStorage', async () => {
    const { mod, documentStub, localStorageStub } = await loadTheme('light')
    const { theme, setTheme } = mod.useTheme()

    setTheme('eva-asuka')
    expect(theme.value).toBe('eva-asuka')
    expect(documentStub.documentElement.setAttribute).toHaveBeenCalledWith('data-theme', 'eva-asuka')
    expect(localStorageStub.setItem).toHaveBeenCalledWith('tl-theme', 'eva-asuka')
  })

  it('localStorage 写入失败被忽略，不影响切换', async () => {
    const { mod, documentStub } = await loadTheme('dark', true)
    const { theme, setTheme } = mod.useTheme()

    expect(() => setTheme('light')).not.toThrow()
    expect(theme.value).toBe('light')
    expect(documentStub.documentElement.setAttribute).toHaveBeenCalledWith('data-theme', 'light')
  })
})

describe('theme 注册表', () => {
  it('内置主题 id 唯一且覆盖声明的类型', async () => {
    const { mod } = await loadTheme('light')
    const ids = mod.THEMES.map((item) => item.id)
    expect(new Set(ids).size).toBe(ids.length)
    expect(ids).toEqual(['light', 'dark', 'eva-01', 'eva-rei', 'eva-asuka'])
  })
})
