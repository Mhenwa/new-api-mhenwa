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
import { describe, expect, it } from 'vitest'

import {
  formatDuration,
  formatRatio,
  getObservationMetricColors,
} from '../lib/format'

describe('service status metric presentation', () => {
  it('changes first-token color at the usage-log ten-second danger boundary', () => {
    expect(getObservationMetricColors({ ttft_ms: 9999 }).firstToken).toBe(
      'text-success'
    )
    expect(getObservationMetricColors({ ttft_ms: 10000 }).firstToken).toBe(
      'text-warning'
    )
  })

  it('changes short-response duration color at the usage-log thirty-second danger boundary', () => {
    expect(
      getObservationMetricColors({ latency_ms: 29999, output_tokens: 1 })
        .totalTime
    ).toBe('text-success')
    expect(
      getObservationMetricColors({ latency_ms: 30000, output_tokens: 1 })
        .totalTime
    ).toBe('text-warning')
  })

  it('uses output throughput for long responses and the time fallback for legacy samples', () => {
    expect(
      getObservationMetricColors({ latency_ms: 30000, output_tokens: 900 })
        .totalTime
    ).toBe('text-success')
    expect(getObservationMetricColors({ latency_ms: 30000 }).totalTime).toBe(
      'text-warning'
    )
  })

  it('keeps absent, zero and invalid timing metrics neutral with an unknown display', () => {
    expect(getObservationMetricColors({}).firstToken).toBe(
      'text-muted-foreground'
    )
    expect(
      getObservationMetricColors({ ttft_ms: 0, latency_ms: Infinity })
    ).toEqual({
      firstToken: 'text-muted-foreground',
      totalTime: 'text-muted-foreground',
    })
    expect(formatDuration(undefined)).toBe('—')
    expect(formatDuration(0)).toBe('—')
  })

  it('preserves zero rates and small effective-rate precision', () => {
    expect(formatRatio(0, 'en')).toBe('×0')
    expect(formatRatio(0.035, 'en')).toBe('×0.035')
    expect(formatRatio(-1, 'en')).toBe('—')
  })
})
