import { useState, useEffect, useCallback } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/ui/card'
import { Badge } from '@/ui/badge'
import { Button } from '@/ui/button'
import { RefreshCw, Activity, AlertTriangle, CheckCircle, XCircle, Clock, Search } from 'lucide-react'
import { cn } from '@/lib/utils'

interface DomainStatusEndpoint {
  healthy: boolean
  ip: string
  failureCount?: number
}

interface DomainStatus {
  lastUpdated: string
  provider: string
  error?: string
  endpoints: DomainStatusEndpoint[]
}

type DomainsResponse = Record<string, DomainStatus>

type Domain = {
  name: string
  status: DomainStatus
}

const DomainMonitorDashboard = ({ endpoint }: { endpoint: string }) => {
  const [domains, setDomains] = useState<Domain[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [searchTerm, setSearchTerm] = useState('')

  const fetchDomains = useCallback(async (): Promise<void> => {
    setIsLoading(true)
    setError(null) // Clear previous errors
    try {
      const response = await fetch(endpoint + '/api/status')
      if (!response.ok) {
        throw new Error(`HTTP error: ${response.status} ${response.statusText}`)
      }
      const data: DomainsResponse = await response.json()

      // Convert the response format to our Domain array
      const domainsArray: Domain[] = Object.entries(data).map(([name, status]) => ({
        name,
        status,
      }))

      setDomains(domainsArray)
      setLastUpdated(new Date())
    } catch (error) {
      console.error('Failed to fetch domains data:', error)
      const errorMessage = error instanceof Error ? error.message : 'Unknown error occurred'
      setError(`Failed to fetch domain data: ${errorMessage}`)

      // Fallback to empty array on error
      setDomains([])
    } finally {
      setIsLoading(false)
    }
  }, [endpoint])

  useEffect(() => {
    if (autoRefresh) {
      fetchDomains()
    }

    const interval = setInterval(() => {
      if (autoRefresh) {
        fetchDomains()
      }
    }, 60000)

    return () => clearInterval(interval)
  }, [autoRefresh, fetchDomains])

  const refreshClicked = async () => {
    await fetchDomains()
  }

  const getDomainStatus = (domain: Domain) => {
    if (domain.status.error) {
      return 'unhealthy'
    }
    if (domain.status.endpoints.length === 0) {
      return 'unhealthy'
    }

    const healthyEndpoints = domain.status.endpoints.filter((e) => e.healthy).length
    const totalEndpoints = domain.status.endpoints.length

    if (healthyEndpoints === totalEndpoints) {
      return 'healthy'
    }
    if (healthyEndpoints > 0) {
      return 'warning'
    }
    return 'unhealthy'
  }

  const filteredDomains = domains.filter((domain) => {
    const searchLower = searchTerm.toLowerCase()
    const domainMatches = domain.name.toLowerCase().includes(searchLower)
    const endpointMatches = domain.status.endpoints.some((endpoint) => endpoint.ip.toLowerCase().includes(searchLower))
    return domainMatches || endpointMatches
  })

  const healthyDomains = filteredDomains.filter((d) => getDomainStatus(d) === 'healthy').length
  const warningDomains = filteredDomains.filter((d) => getDomainStatus(d) === 'warning').length
  const unhealthyDomains = filteredDomains.filter((d) => getDomainStatus(d) === 'unhealthy').length

  return (
    <div className="min-h-screen bg-background p-4 md:p-6">
      <div className="mx-auto max-w-7xl space-y-6">
        {/* Header */}
        <div className="flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
          <div>
            <h1 className="text-3xl font-bold tracking-tight">ddup</h1>
          </div>

          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            {lastUpdated && (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <Clock className="h-4 w-4" />
                Last updated: {lastUpdated.toLocaleTimeString()}
              </div>
            )}

            <div className="flex gap-2">
              <Button
                variant={autoRefresh ? 'default' : 'outline'}
                size="sm"
                onClick={() => setAutoRefresh(!autoRefresh)}
                className="flex items-center gap-2"
              >
                <Activity className="h-4 w-4" />
                Auto-refresh {autoRefresh ? 'ON' : 'OFF'}
              </Button>

              <Button
                variant="outline"
                size="sm"
                onClick={refreshClicked}
                disabled={isLoading}
                className="flex items-center gap-2 bg-transparent"
              >
                <RefreshCw className={cn('h-4 w-4', isLoading && 'animate-spin')} />
                Refresh
              </Button>
            </div>
          </div>
        </div>

        {/* Search Bar */}
        <div className="relative max-w-md">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search domains or IP addresses..."
            value={searchTerm}
            onChange={(e) => setSearchTerm(e.target.value)}
            className="w-full rounded-md border border-input bg-background px-10 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          />
        </div>

        {/* Error Message */}
        {error && (
          <Card className="border-red-200 bg-red-50 lg:w-3/5 mx-auto">
            <CardContent>
              <div className="flex items-center gap-2 text-red-800">
                <XCircle className="h-5 w-5" />
                <span className="font-medium">Error</span>
              </div>
              <p className="mt-2 text-sm text-red-700">{error}</p>
            </CardContent>
          </Card>
        )}

        {/* Stats Overview */}
        {!error && domains.length > 0 && (
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">

            <Card>
              <CardHeader className="flex flex-row items-center justify-between space-y-0">
                <CardTitle className="text-sm font-medium">Healthy Domains</CardTitle>
                <CheckCircle className="h-4 w-4 text-green-600" />
              </CardHeader>
              <CardContent>
                <div className="text-2xl font-bold text-green-600">{healthyDomains}</div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="flex flex-row items-center justify-between space-y-0">
                <CardTitle className="text-sm font-medium">Warning Domains</CardTitle>
                <AlertTriangle className="h-4 w-4 text-yellow-600" />
              </CardHeader>
              <CardContent>
                <div className="text-2xl font-bold text-yellow-600">{warningDomains}</div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="flex flex-row items-center justify-between space-y-0">
                <CardTitle className="text-sm font-medium">Unhealthy Domains</CardTitle>
                <XCircle className="h-4 w-4 text-red-600" />
              </CardHeader>
              <CardContent>
                <div className="text-2xl font-bold text-red-600">{unhealthyDomains}</div>
              </CardContent>
            </Card>
          </div>
        )}

        {/* Domain Cards */}
        {!error && domains.length > 0 && (
          <div className="grid gap-6 md:grid-cols-2 lg:grid-cols-3">
            {filteredDomains.map((domain) => {
              const status = getDomainStatus(domain)
              return (
                <Card key={domain.name} className="overflow-hidden">
                  <CardHeader className="pb-2">
                    <div className="flex items-center justify-between">
                      <CardTitle className="text-lg">{domain.name}</CardTitle>
                      <Badge
                        variant={status === 'healthy' ? 'default' : status === 'warning' ? 'secondary' : 'destructive'}
                        className={cn(
                          'flex items-center gap-1',
                          status === 'healthy' && 'bg-green-100 text-green-800 hover:bg-green-100',
                          status === 'warning' && 'bg-yellow-100 text-yellow-800 hover:bg-yellow-100',
                          status === 'unhealthy' && 'bg-red-100 text-red-800 hover:bg-red-100'
                        )}
                      >
                        {status === 'healthy' && <CheckCircle className="h-3 w-3" />}
                        {status === 'warning' && <AlertTriangle className="h-3 w-3" />}
                        {status === 'unhealthy' && <XCircle className="h-3 w-3" />}
                        {status.charAt(0).toUpperCase() + status.slice(1)}
                      </Badge>
                    </div>
                    <div className="flex items-center justify-between text-xs text-muted-foreground">
                      <span>Provider: {domain.status.provider}</span>
                      <span>
                        <Clock className="inline h-3 w-3 mr-1" />
                        {new Date(domain.status.lastUpdated).toLocaleString()}
                      </span>
                    </div>
                    <div className="text-sm text-muted-foreground">
                      {domain.status.endpoints.filter((e) => e.healthy).length}/{domain.status.endpoints.length} endpoints healthy
                    </div>
                  </CardHeader>

                  <CardContent className="space-y-4">
                    {domain.status.error && (
                      <div className="rounded-lg bg-red-50 p-3 text-sm text-red-800">
                        <div className="flex items-center gap-2">
                          <XCircle className="h-4 w-4" />
                          <span className="font-medium">Domain Error</span>
                        </div>
                        <p className="mt-1">{domain.status.error}</p>
                      </div>
                    )}

                    {/* Endpoints */}
                    {domain.status.endpoints.length > 0 && (
                      <div>
                        <h4 className="mb-2 text-sm font-medium">Endpoints ({domain.status.endpoints.length})</h4>
                        <div className="space-y-2">
                          {domain.status.endpoints.map((endpoint, index) => (
                            <div key={index} className="flex items-center justify-between rounded-lg border p-2">
                              <div className="flex items-center gap-2">
                                <Badge
                                  variant={endpoint.healthy ? 'default' : 'destructive'}
                                  className={cn(
                                    'text-xs',
                                    endpoint.healthy && 'bg-green-100 text-green-800 hover:bg-green-100',
                                    !endpoint.healthy && 'bg-red-100 text-red-800 hover:bg-red-100'
                                  )}
                                >
                                  {endpoint.healthy ? (
                                    <CheckCircle className="h-3 w-3" />
                                  ) : (
                                    <XCircle className="h-3 w-3" />
                                  )}
                                  {endpoint.healthy ? 'Healthy' : 'Unhealthy'}
                                </Badge>
                                <span className="font-mono text-sm">{endpoint.ip}</span>
                              </div>
                              <div className="text-right text-xs text-muted-foreground">
                                <div>Failures: {endpoint.failureCount || '0'}</div>
                              </div>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </CardContent>
                </Card>
              )
            })}
          </div>
        )}

        {/* No Results Message */}
        {filteredDomains.length === 0 && searchTerm && !isLoading && (
          <div className="text-center py-8">
            <p className="text-muted-foreground">No domains or endpoints found matching "{searchTerm}"</p>
          </div>
        )}

        {/* No Domains Message */}
        {domains.length === 0 && !searchTerm && !isLoading && !error && (
          <div className="text-center py-8">
            <p className="text-muted-foreground">No domains configured or available</p>
          </div>
        )}

        {isLoading && (
          <div className="flex items-center justify-center py-8">
            <RefreshCw className="h-6 w-6 animate-spin" />
            <span className="ml-2">Loading domain data...</span>
          </div>
        )}
      </div>
    </div>
  )
}

export { DomainMonitorDashboard }
