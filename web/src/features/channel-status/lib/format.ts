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
import {
  getFirstResponseTimeColor,
  getResponseTimeColor,
} from '@/features/usage-logs/lib/format'

import type { ChannelStatusTarget } from '../types'

export function getObservationMetricColors(
  target: Pick<ChannelStatusTarget, 'ttft_ms' | 'latency_ms' | 'output_tokens'>
): { firstToken: string; totalTime: string } {
  let firstToken = 'text-muted-foreground'
  let totalTime = 'text-muted-foreground'
  if (
    target.ttft_ms != null &&
    Number.isFinite(target.ttft_ms) &&
    target.ttft_ms > 0
  ) {
    firstToken =
      getFirstResponseTimeColor(target.ttft_ms / 1000) === 'danger'
        ? 'text-warning'
        : 'text-success'
  }
  if (
    target.latency_ms != null &&
    Number.isFinite(target.latency_ms) &&
    target.latency_ms > 0
  ) {
    totalTime =
      getResponseTimeColor(
        target.latency_ms / 1000,
        target.output_tokens ?? 0
      ) === 'danger'
        ? 'text-warning'
        : 'text-success'
  }
  return { firstToken, totalTime }
}

export function formatDuration(value: number | undefined): string {
  if (value == null || !Number.isFinite(value) || value <= 0) return '—'
  if (value < 1000) return `${Math.round(value)} ms`
  return `${(value / 1000).toFixed(2)} s`
}

export function formatTimestamp(
  value: number | undefined,
  locale: string | undefined
): string {
  if (value == null || !Number.isFinite(value) || value <= 0) return '-'
  return new Intl.DateTimeFormat(locale, {
    dateStyle: 'short',
    timeStyle: 'medium',
  }).format(new Date(value * 1000))
}

export function formatThroughput(value: number | undefined): string {
  if (value == null || !Number.isFinite(value) || value < 0) return '—'
  return `${value.toFixed(1)} tok/s`
}

export function formatRatio(
  value: number | undefined,
  locale: string | undefined
): string {
  if (value == null || !Number.isFinite(value) || value < 0) return '—'
  return `×${new Intl.NumberFormat(locale, { maximumFractionDigits: 4 }).format(value)}`
}
