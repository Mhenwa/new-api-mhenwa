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
import { Clock3, Gauge, Timer } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { CHANNEL_STATUS_META } from '../constants'
import {
  formatDuration,
  formatThroughput,
  formatTimestamp,
} from '../lib/format'
import type { ChannelStatusTarget } from '../types'

function StatusRow(props: {
  target: ChannelStatusTarget
  locale: string | undefined
}) {
  const { t } = useTranslation()
  const meta = CHANNEL_STATUS_META[props.target.status]
  const labels = {
    healthy: t('Healthy'),
    error: t('Error'),
    probing: t('Probing'),
    no_data: t('No data'),
  }
  let source = t('Not observed')
  if (props.target.source === 'real') source = t('Real request')
  if (props.target.source === 'probe') source = t('Probe')
  const observedTime =
    props.target.observed_at ?? props.target.request_started_at

  return (
    <TableRow>
      <TableCell className='font-medium'>{props.target.group}</TableCell>
      <TableCell>
        <span className='font-mono text-xs'>{props.target.model}</span>
      </TableCell>
      <TableCell>
        <StatusBadge
          label={labels[props.target.status]}
          icon={meta.icon}
          variant={meta.variant}
          pulse={props.target.status === 'probing'}
          copyable={false}
        />
      </TableCell>
      <TableCell className='text-muted-foreground'>{source}</TableCell>
      <TableCell>{formatDuration(props.target.ttft_ms)}</TableCell>
      <TableCell>{formatDuration(props.target.latency_ms)}</TableCell>
      <TableCell>{formatThroughput(props.target.tps)}</TableCell>
      <TableCell className='text-muted-foreground text-xs'>
        {formatTimestamp(observedTime, props.locale)}
      </TableCell>
    </TableRow>
  )
}

export function StatusTable(props: {
  targets: ChannelStatusTarget[]
  locale: string | undefined
}) {
  const { t } = useTranslation()

  return (
    <Table>
      <TableHeader className='bg-background sticky top-0 z-10'>
        <TableRow>
          <TableHead>{t('Group')}</TableHead>
          <TableHead>{t('Model')}</TableHead>
          <TableHead>{t('Status')}</TableHead>
          <TableHead>{t('Request source')}</TableHead>
          <TableHead>
            <span className='inline-flex items-center gap-1'>
              <Timer className='size-3.5' aria-hidden='true' />
              {t('First token')}
            </span>
          </TableHead>
          <TableHead>
            <span className='inline-flex items-center gap-1'>
              <Clock3 className='size-3.5' aria-hidden='true' />
              {t('Total time')}
            </span>
          </TableHead>
          <TableHead>
            <span className='inline-flex items-center gap-1'>
              <Gauge className='size-3.5' aria-hidden='true' />
              {t('Output speed')}
            </span>
          </TableHead>
          <TableHead>{t('Observed at')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.targets.map((target) => (
          <StatusRow
            key={`${target.group}\u0000${target.model}`}
            target={target}
            locale={props.locale}
          />
        ))}
      </TableBody>
    </Table>
  )
}
