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
import { Activity, CircleCheck, Radio, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Card, CardContent } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { formatNumber } from '@/lib/format'

import type { ChannelStatusTarget } from '../types'

export function StatusSummary(props: {
  targets: ChannelStatusTarget[]
  locale?: string
}) {
  const { t } = useTranslation()
  const healthy = props.targets.filter(
    (target) => target.status === 'healthy'
  ).length
  const needsAttention = props.targets.some(
    (target) => target.status === 'error' || target.status === 'degraded'
  )
  let headline = t('Waiting for observations')
  let icon = Radio
  let tone = 'neutral' as 'neutral' | 'success' | 'warning'
  if (needsAttention) {
    headline = t('Some routes need attention')
    icon = Activity
    tone = 'warning'
  } else if (healthy === props.targets.length && healthy > 0) {
    headline = t('All monitored routes are healthy')
    icon = ShieldCheck
    tone = 'success'
  }
  const OverallIcon = icon

  return (
    <section aria-label={t('Status summary')} className='space-y-4'>
      <Card className='relative gap-0 overflow-hidden border-0 py-0 shadow-none'>
        <div
          className='bg-primary/4 pointer-events-none absolute inset-y-0 right-0 w-1/3 rounded-l-full'
          aria-hidden='true'
        />
        <CardContent className='relative flex flex-col gap-5 p-5 sm:flex-row sm:items-center sm:justify-between sm:p-6'>
          <div className='flex min-w-0 items-start gap-3.5 sm:items-center'>
            <IconBadge size='lg' tone={tone} className='size-12 rounded-2xl'>
              <OverallIcon />
            </IconBadge>
            <div className='min-w-0'>
              <p className='text-muted-foreground mb-1 text-[11px] font-semibold tracking-widest uppercase'>
                {t('Live route monitoring')}
              </p>
              <h3 className='text-lg font-semibold tracking-tight sm:text-xl'>
                {headline}
              </h3>
              <p className='text-muted-foreground mt-1.5 max-w-xl text-xs leading-relaxed sm:text-sm'>
                {t('Real traffic is preferred; probes fill idle periods.')}
              </p>
            </div>
          </div>
          <div className='bg-background/80 flex h-auto items-center justify-start gap-3 rounded-xl border px-4 py-3 sm:min-w-36'>
            <IconBadge tone='primary' size='md'>
              <CircleCheck />
            </IconBadge>
            <span className='text-left'>
              <span className='block font-mono text-xl font-semibold tabular-nums'>
                {formatNumber(props.targets.length, props.locale)}
              </span>
              <span className='text-muted-foreground block text-xs'>
                {t('Monitored routes')}
              </span>
            </span>
          </div>
        </CardContent>
      </Card>
    </section>
  )
}
