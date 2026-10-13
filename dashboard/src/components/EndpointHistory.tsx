import { useCallback, useEffect, useId, useLayoutEffect, useRef, type ReactNode } from 'react'
import { CheckCircle, History, X, XCircle } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface EndpointHistoryItem {
  healthy: boolean
  ip: string
  since?: string
  sinceStart?: boolean
  lastFailure?: string
}

// Short local date and time, for example "Oct 9, 8:15 AM", with the year only for times that are not in the current year
const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const timeFormatThisYear = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
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

const EndpointStatus = ({ endpoint }: { endpoint: EndpointHistoryItem }) => {
  const state = endpoint.healthy ? 'Healthy' : 'Unhealthy'
  let detail: ReactNode = null
  if (endpoint.since && endpoint.sinceStart) {
    detail = (
      <>
        {' '}
        since ddup started (<Time value={endpoint.since} />)
      </>
    )
  } else if (endpoint.since) {
    detail = (
      <>
        {' '}
        since <Time value={endpoint.since} />
      </>
    )
  }

  // The icon has a different shape for each state, and the text says the same, so the state doesn't depend on color
  const Icon = endpoint.healthy ? CheckCircle : XCircle
  return (
    <>
      <div
        className={cn(
          'flex items-start gap-1.5',
          endpoint.healthy ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'
        )}
      >
        <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <span>
          {state}
          {detail}
        </span>
      </div>
      {endpoint.healthy && endpoint.lastFailure && (
        <div className="pl-[22px] text-muted-foreground">
          Last failure: <Time value={endpoint.lastFailure} />
        </div>
      )}
    </>
  )
}

// A button that opens a dialog with the history of the endpoints of a domain
// The dialog is not modal, and it is not a focus trap: focus moves into it when it opens, and closing it (Escape, the close button, a click outside, or moving focus out of it) is always possible
// It is placed right after the button in the DOM, so the tab order is natural, and it uses fixed positioning so it's not clipped by the card
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

  const close = useCallback(
    (returnFocus: boolean) => {
      onOpenChange(false)
      if (returnFocus) {
        buttonRef.current?.focus()
      }
    },
    [onOpenChange]
  )

  // Places the dialog under the button, aligned to its right edge, or above it if there is more room there
  // It's always inside the viewport, also on narrow screens
  const place = useCallback(() => {
    const button = buttonRef.current
    const dialog = dialogRef.current
    if (!button || !dialog) {
      return
    }

    const anchor = button.getBoundingClientRect()
    const viewportWidth = document.documentElement.clientWidth
    const viewportHeight = window.innerHeight

    const width = Math.min(popoverWidth, viewportWidth - 2 * viewportMargin)
    const left = Math.max(viewportMargin, Math.min(anchor.right - width, viewportWidth - width - viewportMargin))

    const roomBelow = viewportHeight - anchor.bottom - buttonGap - viewportMargin
    const roomAbove = anchor.top - buttonGap - viewportMargin
    const placeAbove = dialog.scrollHeight > roomBelow && roomAbove > roomBelow
    const maxHeight = Math.max(placeAbove ? roomAbove : roomBelow, 120)
    const height = Math.min(dialog.scrollHeight, maxHeight)

    dialog.style.width = `${width}px`
    dialog.style.left = `${left}px`
    dialog.style.maxHeight = `${maxHeight}px`
    dialog.style.top = `${placeAbove ? anchor.top - buttonGap - height : anchor.bottom + buttonGap}px`
  }, [])

  // Place the dialog before it's painted, and keep it next to the button when the page moves or changes size
  useLayoutEffect(() => {
    if (!open) {
      return
    }

    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, { capture: true, passive: true })
    return () => {
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place, { capture: true })
    }
  }, [open, place, endpoints])

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
          'inline-flex min-h-7 min-w-7 cursor-pointer items-center justify-center gap-1 rounded-md px-1.5 text-xs font-medium text-foreground',
          'outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50',
          open && 'bg-accent'
        )}
      >
        <History className="h-4 w-4" aria-hidden="true" />
        History
      </button>

      {open && (
        <div
          ref={dialogRef}
          id={dialogId}
          role="dialog"
          aria-labelledby={titleId}
          tabIndex={-1}
          className="fixed z-50 overflow-y-auto overscroll-contain rounded-lg border bg-popover p-4 text-sm text-popover-foreground shadow-lg outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <div className="flex items-start justify-between gap-2">
            <h2 id={titleId} className="min-w-0 break-words font-semibold">
              Endpoint history of {domain}
            </h2>
            <button
              type="button"
              aria-label="Close"
              onClick={() => close(true)}
              className="-mr-1.5 -mt-1 inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
            >
              <X className="h-4 w-4" aria-hidden="true" />
            </button>
          </div>

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
