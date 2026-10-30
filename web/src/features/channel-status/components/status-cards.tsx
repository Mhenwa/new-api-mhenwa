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
import { Clock3, Coins, Radio, Timer, Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Card, CardContent } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { cn } from '@/lib/utils'

import { CHANNEL_STATUS_META } from '../constants'
import {
  formatDuration,
  formatRatio,
  getObservationMetricColors,
} from '../lib/format'
import type { ChannelStatusTarget } from '../types'
import { RouteHistory } from './route-history'

export function StatusCards(props: {
  targets: ChannelStatusTarget[]
  locale: string | undefined
}) {
  const { t } = useTranslation()
  const labels = {
    healthy: t('Healthy'),
    error: t('Error'),
    degraded: t('High latency'),
    no_data: t('No data'),
  }
  return (
    <div className='grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3'>
      {props.targets.map((target) => {
        const metricColors = getObservationMetricColors(target)
        const meta = CHANNEL_STATUS_META[target.status]
        const Icon = meta.icon
        const sourceIcon = target.source === 'real' ? Users : Radio
        const SourceIcon = sourceIcon
        let source = t('Not observed')
        if (target.source === 'real') source = t('Real request')
        if (target.source === 'probe') source = t('Probe')
        const tones = {
          healthy: 'success',
          error: 'destructive',
          degraded: 'warning',
          no_data: 'neutral',
        } as const
        const metrics = [
          {
            label: t('First token'),
            icon: Timer,
            value: formatDuration(target.ttft_ms),
            highlight: true,
          },
          {
            label: t('Effective rate'),
            icon: Coins,
            value: formatRatio(target.effective_ratio, props.locale),
            highlight: false,
          },
        ]
        return (
          <article
            key={`${target.group}\u0000${target.model}`}
            aria-label={`${target.group} / ${target.model}`}
            className='min-w-0'
          >
            <Card
              className={cn(
                'h-full gap-0 overflow-hidden py-0 shadow-none transition-shadow hover:shadow-sm',
                target.status === 'error' && 'ring-destructive/30'
              )}
            >
              <CardContent className='space-y-4 p-4 sm:p-5'>
                <div className='flex min-w-0 items-start justify-between gap-2'>
                  <div className='flex min-w-0 items-start gap-2.5'>
                    <IconBadge size='lg' tone={tones[target.status]}>
                      <Icon />
                    </IconBadge>
                    <div className='min-w-0'>
                      <h4
                        className='truncate text-sm font-semibold sm:text-base'
                        title={target.group}
                      >
                        {target.group}
                      </h4>
                      <p className='text-muted-foreground mt-1 font-mono text-xs leading-relaxed break-all'>
                        {target.model}
                      </p>
                    </div>
                  </div>
                  <StatusBadge
                    label={labels[target.status]}
                    title={
                      target.status === 'degraded'
                        ? t('Latency data is incomplete or latency is high.')
                        : undefined
                    }
                    icon={meta.icon}
                    variant={meta.variant}
                    copyable={false}
                    className='shrink-0 text-[11px]'
                  />
                </div>
                <div className='grid grid-cols-2 gap-2.5'>
                  {metrics.map((metric) => (
                    <div
                      key={metric.label}
                      className={cn(
                        'min-w-0 rounded-xl border p-3',
                        metric.highlight
                          ? 'border-primary/10 bg-primary/4'
                          : 'bg-muted/35 border-border/60'
                      )}
                    >
                      <div className='text-muted-foreground flex items-center gap-1.5 text-[11px]'>
                        <metric.icon className='size-3.5' aria-hidden='true' />
                        {metric.label}
                      </div>
                      <p
                        className={cn(
                          'mt-2 font-mono text-lg font-semibold tracking-tight tabular-nums sm:text-xl',
                          metric.highlight && metricColors.firstToken
                        )}
                      >
                        {metric.value}
                      </p>
                    </div>
                  ))}
                </div>
                <div className='flex flex-wrap items-center justify-between gap-x-3 gap-y-2 text-xs'>
                  <span className='text-muted-foreground inline-flex items-center gap-1.5'>
                    <Clock3 className='size-3.5' aria-hidden='true' />
                    {t('Total time')}
                    <strong
                      className={cn(
                        'font-mono font-medium tabular-nums',
                        metricColors.totalTime
                      )}
                    >
                      {formatDuration(target.latency_ms)}
                    </strong>
                  </span>
                  <span className='bg-muted/60 text-muted-foreground inline-flex items-center gap-1.5 rounded-md px-2 py-1'>
                    <SourceIcon className='size-3' aria-hidden='true' />
                    {source}
                  </span>
                </div>
                <RouteHistory history={target.history} locale={props.locale} />
              </CardContent>
            </Card>
          </article>
        )
      })}
    </div>
  )
}
