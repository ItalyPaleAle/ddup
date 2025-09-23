import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { DomainMonitorDashboard } from '@/components/DomainMonitorDashboard'

import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <DomainMonitorDashboard endpoint="" />
  </StrictMode>
)
