import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { SidebarProvider, SidebarTrigger, useSidebar } from './sidebar'

const COOKIE = 'sidebar_state'

function clearCookie() {
  document.cookie = `${COOKIE}=; path=/; max-age=0`
}

function readCookie(): string | undefined {
  return document.cookie
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith(`${COOKIE}=`))
    ?.split('=')[1]
}

/** Surfaces the context state, and exposes setOpen for the batching test. */
function Probe() {
  const { state, setOpen } = useSidebar()
  return (
    <div>
      <span data-testid="state">{state}</span>
      {/* Two dispatches in ONE handler — the batching case the old captured
          `open` collapsed into a single effective toggle. */}
      <button type="button" onClick={() => { setOpen(false); setOpen(true) }}>
        off then on
      </button>
      <button type="button" onClick={() => { setOpen((v) => !v); setOpen((v) => !v) }}>
        toggle twice
      </button>
    </div>
  )
}

beforeEach(clearCookie)
afterEach(clearCookie)

describe('SidebarProvider collapsed-state persistence', () => {
  it('defaults to open when nothing is persisted', () => {
    render(
      <SidebarProvider>
        <Probe />
      </SidebarProvider>,
    )
    expect(screen.getByTestId('state')).toHaveTextContent('expanded')
  })

  it('writes the collapse to the cookie when toggled', async () => {
    const user = userEvent.setup()
    render(
      <SidebarProvider>
        <SidebarTrigger />
        <Probe />
      </SidebarProvider>,
    )

    await user.click(screen.getByRole('button', { name: /toggle sidebar/i }))

    expect(screen.getByTestId('state')).toHaveTextContent('collapsed')
    expect(readCookie()).toBe('false')
  })

  // The regression: the cookie was written but never read, so every reload and
  // every full-page navigation (the sign-in redirect included) silently
  // re-expanded a sidebar the operator had collapsed.
  it('restores a persisted collapsed state on a fresh mount', () => {
    document.cookie = `${COOKIE}=false; path=/`

    render(
      <SidebarProvider>
        <Probe />
      </SidebarProvider>,
    )

    expect(screen.getByTestId('state')).toHaveTextContent('collapsed')
  })

  it('restores a persisted expanded state over a collapsed default', () => {
    document.cookie = `${COOKIE}=true; path=/`

    render(
      <SidebarProvider defaultOpen={false}>
        <Probe />
      </SidebarProvider>,
    )

    expect(screen.getByTestId('state')).toHaveTextContent('expanded')
  })

  it('falls back to defaultOpen when the cookie value is not a known literal', () => {
    document.cookie = `${COOKIE}=maybe; path=/`

    render(
      <SidebarProvider defaultOpen={false}>
        <Probe />
      </SidebarProvider>,
    )

    expect(screen.getByTestId('state')).toHaveTextContent('collapsed')
  })

  it('survives an unmount/remount cycle without losing the collapse', async () => {
    const user = userEvent.setup()
    const first = render(
      <SidebarProvider>
        <SidebarTrigger />
        <Probe />
      </SidebarProvider>,
    )

    await user.click(screen.getByRole('button', { name: /toggle sidebar/i }))
    expect(screen.getByTestId('state')).toHaveTextContent('collapsed')
    first.unmount()

    render(
      <SidebarProvider>
        <Probe />
      </SidebarProvider>,
    )
    expect(screen.getByTestId('state')).toHaveTextContent('collapsed')
  })
})

describe('SidebarProvider toggle dispatch', () => {
  // Guards the dropped-toggle bug: `setOpen` used to resolve the next value from
  // a captured `open`, so a second dispatch landing before the re-render computed
  // from the stale value and the sidebar refused to move.
  it('applies both values when setOpen is dispatched twice in one handler', async () => {
    const user = userEvent.setup()
    render(
      <SidebarProvider>
        <Probe />
      </SidebarProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'off then on' }))

    expect(screen.getByTestId('state')).toHaveTextContent('expanded')
    expect(readCookie()).toBe('true')
  })

  it('applies both updaters when toggled twice in one handler', async () => {
    const user = userEvent.setup()
    render(
      <SidebarProvider>
        <Probe />
      </SidebarProvider>,
    )

    // open → closed → open. A stale read would have produced `closed` twice and
    // settled on collapsed.
    await user.click(screen.getByRole('button', { name: 'toggle twice' }))

    expect(screen.getByTestId('state')).toHaveTextContent('expanded')
  })

  it('keeps the cmd/ctrl+B shortcut working across repeated toggles', async () => {
    render(
      <SidebarProvider>
        <Probe />
      </SidebarProvider>,
    )

    const press = () =>
      act(() => {
        window.dispatchEvent(
          new KeyboardEvent('keydown', { key: 'b', ctrlKey: true, bubbles: true }),
        )
      })

    // `setOpen` used to be rebuilt on every toggle, which tore down and
    // re-registered this listener each time; the third press is what caught a
    // listener that had gone stale.
    press()
    expect(screen.getByTestId('state')).toHaveTextContent('collapsed')
    press()
    expect(screen.getByTestId('state')).toHaveTextContent('expanded')
    press()
    expect(screen.getByTestId('state')).toHaveTextContent('collapsed')
  })
})
