import { layersFor, type IconLayer, type OverallStatus } from '@/lib/favicon-shapes'

// The icon of the tab as an inline SVG, drawn from the same shapes as the pre-rendered PNGs
export const StatusIcon = ({ status, className }: { status: OverallStatus; className?: string }) => (
  <svg
    viewBox="0 0 64 64"
    role="img"
    aria-label={`Overall status: ${status}`}
    className={className}
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    {layersFor(status).map((layer: IconLayer, i) => (
      <path key={i} d={layer.d} fill={layer.fill ?? 'none'} stroke={layer.stroke} strokeWidth={layer.strokeWidth} />
    ))}
  </svg>
)
