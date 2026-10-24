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
export type ChannelStatus = 'healthy' | 'error' | 'probing' | 'no_data'

export type ChannelStatusSource = 'real' | 'probe' | 'none'

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
