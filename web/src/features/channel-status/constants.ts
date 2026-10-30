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
  CircleAlert,
  CircleCheck,
  CircleHelp,
  Clock3,
  type LucideIcon,
} from 'lucide-react'

import type { ChannelStatus } from './types'

export const CHANNEL_STATUS_ORDER: ChannelStatus[] = [
  'healthy',
  'error',
  'degraded',
  'no_data',
]

export const CHANNEL_STATUS_META: Record<
  ChannelStatus,
  { icon: LucideIcon; variant: 'success' | 'danger' | 'warning' | 'neutral' }
> = {
  healthy: { icon: CircleCheck, variant: 'success' },
  error: { icon: CircleAlert, variant: 'danger' },
  degraded: { icon: Clock3, variant: 'warning' },
  no_data: { icon: CircleHelp, variant: 'neutral' },
}
