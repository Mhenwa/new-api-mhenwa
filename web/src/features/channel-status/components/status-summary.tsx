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
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'

import { CHANNEL_STATUS_META, CHANNEL_STATUS_ORDER } from '../constants'
import type { ChannelStatus, ChannelStatusTarget } from '../types'

export function StatusSummary(props: { targets: ChannelStatusTarget[] }) {
  const { t } = useTranslation()
  const counts: Record<ChannelStatus, number> = {
    healthy: 0,
    error: 0,
    probing: 0,
    no_data: 0,
  }
  const labels: Record<ChannelStatus, string> = {
    healthy: t('Healthy'),
    error: t('Error'),
    probing: t('Probing'),
    no_data: t('No data'),
  }
  for (const target of props.targets) counts[target.status] += 1

  return (
    <div
      className='flex flex-wrap items-center gap-x-4 gap-y-2 border-b px-3 py-2.5 sm:px-4'
      aria-label={t('Status summary')}
    >
      <span className='text-muted-foreground text-xs'>
        {t('{{count}} routes', { count: props.targets.length })}
      </span>
      {CHANNEL_STATUS_ORDER.map((status) => {
        const meta = CHANNEL_STATUS_META[status]
        return (
          <StatusBadge
            key={status}
            label={`${labels[status]} ${counts[status]}`}
            icon={meta.icon}
            variant={meta.variant}
            copyable={false}
            className='text-xs'
          />
        )
      })}
    </div>
  )
}
