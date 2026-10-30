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
  ChannelHistory,
  ChannelObservation,
  ChannelStatus,
  ChannelStatusData,
  ChannelStatusResponse,
  ChannelStatusSource,
  ChannelStatusTarget,
} from './types'

const statuses = new Set<ChannelStatus>([
  'healthy',
  'error',
  'degraded',
  'no_data',
])
const sources = new Set<ChannelStatusSource>(['real', 'probe', 'none'])

function finiteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function nonNegative(value: unknown): number | undefined {
  const number = finiteNumber(value)
  return number != null && number >= 0 ? number : undefined
}

function count(value: unknown): number | undefined {
  const number = nonNegative(value)
  return number != null && Number.isSafeInteger(number) ? number : undefined
}

function normalizeObservations(
  value: unknown,
  limit: number,
  realOnly = false
): ChannelObservation[] {
  if (!Array.isArray(value)) return []
  const observations: ChannelObservation[] = []
  for (const raw of value) {
    if (!raw || typeof raw !== 'object') continue
    const sample = raw as Record<string, unknown>
    const at = count(sample.observed_at)
    const order = count(sample.order_ms)
    if (
      typeof sample.id !== 'string' ||
      sample.id.trim().length === 0 ||
      (sample.source !== 'real' && sample.source !== 'probe') ||
      (realOnly && sample.source !== 'real') ||
      (sample.status !== 'healthy' &&
        sample.status !== 'degraded' &&
        sample.status !== 'error') ||
      at == null ||
      at <= 0 ||
      order == null ||
      order <= 0
    ) {
      continue
    }
    observations.push({
      id: sample.id,
      source: sample.source,
      status: sample.status,
      observed_at: at,
      order_ms: order,
      request_started_at: nonNegative(sample.request_started_at),
      ttft_ms: nonNegative(sample.ttft_ms),
      latency_ms: nonNegative(sample.latency_ms),
      output_tokens: count(sample.output_tokens),
    })
  }
  const seen = new Set<string>()
  return observations
    .sort((a, b) => b.order_ms - a.order_ms)
    .filter((sample) => {
      if (seen.has(sample.id)) return false
      seen.add(sample.id)
      return true
    })
    .slice(0, limit)
}

function normalizeHistory(value: unknown): ChannelHistory | undefined {
  if (!value || typeof value !== 'object') return undefined
  const history = value as Record<string, unknown>
  if (
    !Array.isArray(history.observations) ||
    !Array.isArray(history.real_samples)
  ) {
    return undefined
  }
  return {
    observation_limit: 60,
    real_sample_limit: 30,
    observations: normalizeObservations(history.observations, 60),
    real_samples: normalizeObservations(history.real_samples, 30, true),
  }
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
    output_tokens: count(target.output_tokens),
    effective_ratio: nonNegative(target.effective_ratio),
    history: normalizeHistory(target.history),
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
    disableDuplicate: true,
  })
  return {
    success: res.data?.success === true,
    message: res.data?.message,
    data: normalizeData(res.data?.data),
  }
}
