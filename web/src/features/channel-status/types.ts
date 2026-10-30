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
export type ChannelStatus = 'healthy' | 'degraded' | 'error' | 'no_data'

export type ChannelStatusSource = 'real' | 'probe' | 'none'

export interface ChannelObservation {
  id: string
  source: 'real' | 'probe'
  status: 'healthy' | 'degraded' | 'error'
  observed_at: number
  order_ms: number
  request_started_at?: number
  ttft_ms?: number
  latency_ms?: number
  output_tokens?: number
}

export interface ChannelHistory {
  observation_limit: number
  real_sample_limit: number
  observations: ChannelObservation[]
  real_samples: ChannelObservation[]
}

export interface ChannelStatusTarget {
  group: string
  model: string
  status: ChannelStatus
  source: ChannelStatusSource
  observed_at?: number
  request_started_at?: number
  ttft_ms?: number
  latency_ms?: number
  tps?: number
  output_tokens?: number
  effective_ratio?: number
  history?: ChannelHistory
}

export interface ChannelStatusData {
  updated_at: number
  targets: ChannelStatusTarget[]
}

export interface ChannelStatusResponse {
  success: boolean
  message?: string
  data?: ChannelStatusData
}
