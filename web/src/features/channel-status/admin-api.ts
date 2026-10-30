import { api } from '@/lib/api'

export type ChannelStatusMonitorTarget = {
  group: string
  model: string
  probe_token_id: number
  interval_seconds: number
  timeout_seconds: number
}

export type ChannelStatusProbeToken = {
  id: number
  name?: string
  user_id?: number
  group: string
  models?: string[]
}

export type ChannelStatusMonitorData = {
  targets: ChannelStatusMonitorTarget[]
  source?: 'database' | 'environment' | 'none' | string
  groups: string[]
  models_by_group: Record<string, string[]>
  probe_tokens: ChannelStatusProbeToken[]
}

type ChannelStatusMonitorResponse = {
  success: boolean
  message?: string
  data?: ChannelStatusMonitorData
}

export async function getChannelStatusMonitor(
  signal?: AbortSignal
): Promise<ChannelStatusMonitorResponse> {
  const response = await api.get<ChannelStatusMonitorResponse>(
    '/api/admin/channel-status/targets',
    { signal, disableDuplicate: true }
  )
  return response.data
}

export async function updateChannelStatusMonitor(
  targets: ChannelStatusMonitorTarget[]
): Promise<ChannelStatusMonitorResponse> {
  const response = await api.put<ChannelStatusMonitorResponse>(
    '/api/admin/channel-status/targets',
    { targets },
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return response.data
}
