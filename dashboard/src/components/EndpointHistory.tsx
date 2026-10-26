import { useCallback, useEffect, useId, useLayoutEffect, useRef, type ReactNode } from 'react'
import { AlertTriangle, CheckCircle, History, XCircle } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface EndpointHistoryItem {
  healthy: boolean
  ip: string
  since?: string
  sinceStart?: boolean
  failureCount?: number
  lastFailure?: string
  lastError?: string
  firstFailure?: string
}

// Short local date and time with seconds, for example "Oct 9, 8:15:30 AM", with the year only for times that are not in the current year
const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' })
const timeFormatThisYear = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
  second: '2-digit',
})

const Time = ({ value }: { value: string }) => {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return <>{value}</>
  }

  const format = date.getFullYear() === new Date().getFullYear() ? timeFormatThisYear : timeFormat
  return (
    <time dateTime={value} className="whitespace-nowrap">
      {format.format(date)}
    </time>
  )
}

// Distance in px kept between the popover and the edges of the viewport, and between the popover and its button
const viewportMargin = 8
const buttonGap = 6
const popoverWidth = 340

const Failure = ({ label, time, error }: { label: string; time: string; error?: string }) => (
  <div className="pl-[22px] text-muted-foreground">
    {label}: <Time value={time} />
    {error && <div className="break-words font-mono text-xs">{error}</div>}
  </div>
)

const EndpointStatus = ({ endpoint }: { endpoint: EndpointHistoryItem }) => {
  const failureCount = endpoint.failureCount ?? 0
  // A healthy endpoint with failed checks is still in DNS, but is one more failure away from being removed
  const warning = endpoint.healthy && failureCount > 0
  const state = warning ? 'Warning' : endpoint.healthy ? 'Healthy' : 'Unhealthy'

  let since: ReactNode = null
  if (endpoint.since && endpoint.sinceStart) {
    since = (
      <>
        since ddup started (<Time value={endpoint.since} />)
      </>
    )
  } else if (endpoint.since) {
    since = (
      <>
        since <Time value={endpoint.since} />
      </>
    )
  }

  // The icon has a different shape for each state, and the text says the same, so the state doesn't depend on color
  const Icon = warning ? AlertTriangle : endpoint.healthy ? CheckCircle : XCircle
  const color = warning
    ? 'text-yellow-700 dark:text-yellow-400'
    : endpoint.healthy
      ? 'text-green-700 dark:text-green-400'
      : 'text-red-700 dark:text-red-400'

  // The most recent failure goes first
  // The first failure is only different from the last one when more than one check failed
  const showFirst = failureCount > 1 && endpoint.firstFailure
  return (
    <>
      <div className={cn('flex items-start gap-1.5', color)}>
        <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <span>
          {state}
          {warning ? (
            <>
              : {failureCount} failed {failureCount === 1 ? 'check' : 'checks'} in a row
            </>
          ) : (
            since && <> {since}</>
          )}
        </span>
      </div>
      {!warning && !endpoint.healthy && failureCount > 0 && (
        <div className="pl-[22px] text-muted-foreground">
          {failureCount} failed {failureCount === 1 ? 'check' : 'checks'} in a row
        </div>
      )}
      {endpoint.lastFailure && <Failure label="Last failure" time={endpoint.lastFailure} error={endpoint.lastError} />}
      {showFirst && <Failure label="First failure" time={endpoint.firstFailure!} />}
    </>
  )
}

// An icon button that opens a dialog with the history of the endpoints of a domain
// The dialog is not modal, and it is not a focus trap: focus moves into it when it opens, and closing it (Escape, the button, a click outside, or moving focus out of it) is always possible
// It is placed right after the button in the DOM, so the tab order is natural
const EndpointHistory = ({
  domain,
  lastUpdated,
  endpoints,
  open,
  onOpenChange,
}: {
  domain: string
  lastUpdated: string
  endpoints: EndpointHistoryItem[]
  // The parent keeps the state, so only one dialog is open at a time
  open: boolean
  onOpenChange: (open: boolean) => void
}) => {
  const id = useId()
  const dialogId = `${id}-dialog`
  const titleId = `${id}-title`
  const buttonRef = useRef<HTMLButtonElement>(null)
  const dialogRef = useRef<HTMLDivElement>(null)
  // True between a pointer press on the button and the click it causes, so the press is not mistaken for the keyboard moving focus to the button
  const pressingButton = useRef(false)
  // Whether the dialog is above the button, chosen when it opens, so it doesn't jump to the other side afterwards
  const placedAbove = useRef<boolean | null>(null)

  const close = useCallback(
    (returnFocus: boolean) => {
      onOpenChange(false)
      if (returnFocus) {
        buttonRef.current?.focus()
      }
    },
    [onOpenChange]
  )

  // Places the dialog under the button, aligned to its right edge, or above it if there is more room there when it opens
  // When it opens, it's inside the viewport, also on narrow screens
  const place = useCallback(() => {
    const button = buttonRef.current
    const dialog = dialogRef.current
    if (!button || !dialog) {
      return
    }

    const anchor = button.getBoundingClientRect()
    const viewportWidth = document.documentElement.clientWidth

    const width = Math.min(popoverWidth, viewportWidth - 2 * viewportMargin)
    const left = Math.max(viewportMargin, Math.min(anchor.right - width, viewportWidth - width - viewportMargin))
    dialog.style.width = `${width}px`

    if (placedAbove.current === null) {
      const roomBelow = window.innerHeight - anchor.bottom - buttonGap - viewportMargin
      const roomAbove = anchor.top - buttonGap - viewportMargin
      placedAbove.current = dialog.scrollHeight > roomBelow && roomAbove > roomBelow
      dialog.style.maxHeight = `${Math.max(placedAbove.current ? roomAbove : roomBelow, 120)}px`
    }
    const height = dialog.offsetHeight

    // The coordinates are relative to the page, so find where the page's origin is in the viewport
    dialog.style.left = '0px'
    dialog.style.top = '0px'
    const origin = dialog.getBoundingClientRect()
    dialog.style.left = `${left - origin.left}px`
    dialog.style.top = `${(placedAbove.current ? anchor.top - buttonGap - height : anchor.bottom + buttonGap) - origin.top}px`
  }, [])

  // Place the dialog before it's painted, and again after every render, in case the layout moved the button
  // Scrolling moves the dialog together with the page, so it doesn't need to be placed again
  useLayoutEffect(() => {
    if (!open) {
      placedAbove.current = null
      return
    }

    place()
  })

  // Keep the dialog next to the button and inside the viewport when the window changes size
  useEffect(() => {
    if (!open) {
      return
    }

    window.addEventListener('resize', place)
    return () => {
      window.removeEventListener('resize', place)
    }
  }, [open, place])

  // Move focus into the dialog when it opens
  useEffect(() => {
    if (open) {
      dialogRef.current?.focus({ preventScroll: true })
    }
  }, [open])

  useEffect(() => {
    if (!open) {
      return
    }

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        close(true)
      }
    }

    // A press outside of the dialog and its button closes it, and focus goes wherever the press puts it
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node
      if (dialogRef.current?.contains(target) || buttonRef.current?.contains(target)) {
        return
      }

      const hadFocus = dialogRef.current?.contains(document.activeElement) ?? false
      close(false)

      // If the press doesn't land on anything that takes focus, don't leave focus on the page body
      if (hadFocus) {
        setTimeout(() => {
          if (document.activeElement === document.body) {
            buttonRef.current?.focus()
          }
        }, 0)
      }
    }

    // Moving focus out of the dialog, for example with Tab or Shift+Tab, closes it
    const onFocusIn = (event: FocusEvent) => {
      const target = event.target as Node
      const pressed = pressingButton.current
      pressingButton.current = false
      if (dialogRef.current?.contains(target)) {
        return
      }

      // Pressing the button moves focus to it, and then its click closes the dialog
      // Shift+Tab from the dialog to the button is different: it leaves the dialog, so it closes it
      const fromDialog = dialogRef.current?.contains(event.relatedTarget as Node | null)
      if (target === buttonRef.current && (pressed || !fromDialog)) {
        return
      }
      close(false)
    }

    document.addEventListener('keydown', onKeyDown)
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('focusin', onFocusIn)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('focusin', onFocusIn)
    }
  }, [open, close])

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        aria-label={`History of ${domain}`}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? dialogId : undefined}
        title="Endpoint history"
        onPointerDown={() => {
          pressingButton.current = true
        }}
        onClick={() => {
          pressingButton.current = false
          onOpenChange(!open)
        }}
        className={cn(
          'inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground',
          'outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50',
          open && 'bg-accent text-accent-foreground'
        )}
      >
        <History className="h-4 w-4" aria-hidden="true" />
      </button>

      {open && (
        <div
          ref={dialogRef}
          id={dialogId}
          role="dialog"
          aria-labelledby={titleId}
          tabIndex={-1}
          className="absolute z-50 overflow-y-auto overscroll-contain rounded-lg border bg-popover p-4 text-sm text-popover-foreground shadow-lg outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <h2 id={titleId} className="wrap-break-word font-semibold">
            Endpoint history of {domain}
          </h2>

          <p className="mt-1 text-muted-foreground">
            Last updated: <Time value={lastUpdated} />
          </p>

          {endpoints.length === 0 ? (
            <p className="mt-3">No endpoints</p>
          ) : (
            <ul className="mt-3 space-y-3">
              {endpoints.map((endpoint) => (
                <li key={endpoint.ip} className="space-y-0.5 border-t pt-3">
                  <div className="break-all font-mono">{endpoint.ip}</div>
                  <EndpointStatus endpoint={endpoint} />
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </>
  )
}

export { EndpointHistory }
