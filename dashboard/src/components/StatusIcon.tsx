import { badgeLayers, iconLayers, type IconLayer, type OverallStatus } from '@/lib/favicon'

// The icon of the tab as an inline SVG, drawn from the same shapes
export const StatusIcon = ({ status, className }: { status: OverallStatus; className?: string }) => (
  <svg
    viewBox="0 0 64 64"
    role="img"
    aria-label={`Overall status: ${status}`}
    className={className}
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    {[...iconLayers, ...badgeLayers(status)].map((layer: IconLayer, i) => (
      <path key={i} d={layer.d} fill={layer.fill ?? 'none'} stroke={layer.stroke} strokeWidth={layer.strokeWidth} />
    ))}
  </svg>
)
