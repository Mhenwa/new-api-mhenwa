/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from '@/lib/api'

import type {
  ChannelStatus,
  ChannelStatusData,
  ChannelStatusResponse,
  ChannelStatusSource,
  ChannelStatusTarget,
} from './types'

const statuses = new Set<ChannelStatus>([
  'healthy',
  'error',
  'probing',
  'no_data',
])
const sources = new Set<ChannelStatusSource>(['real', 'probe', 'none'])

function finiteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function normalizeTarget(value: unknown): ChannelStatusTarget | null {
  if (!value || typeof value !== 'object') return null
  const target = value as Record<string, unknown>
  if (typeof target.group !== 'string' || typeof target.model !== 'string') {
    return null
  }
  return {
    group: target.group,
    model: target.model,
    status: statuses.has(target.status as ChannelStatus)
      ? (target.status as ChannelStatus)
      : 'no_data',
    source: sources.has(target.source as ChannelStatusSource)
      ? (target.source as ChannelStatusSource)
      : 'none',
    observed_at: finiteNumber(target.observed_at),
    request_started_at: finiteNumber(target.request_started_at),
    ttft_ms: finiteNumber(target.ttft_ms),
    latency_ms: finiteNumber(target.latency_ms),
    tps: finiteNumber(target.tps),
  }
}

function normalizeData(value: unknown): ChannelStatusData | undefined {
  if (!value || typeof value !== 'object') return undefined
  const data = value as Record<string, unknown>
  const rawTargets = Array.isArray(data.targets) ? data.targets : []
  return {
    updated_at: finiteNumber(data.updated_at) ?? 0,
    targets: rawTargets
      .map(normalizeTarget)
      .filter((target): target is ChannelStatusTarget => target !== null),
  }
}

export async function getChannelStatus(
  signal?: AbortSignal
): Promise<ChannelStatusResponse> {
  const res = await api.get<ChannelStatusResponse>('/api/channel-status', {
    signal,
  })
  return {
    success: res.data?.success === true,
    message: res.data?.message,
    data: normalizeData(res.data?.data),
  }
}
