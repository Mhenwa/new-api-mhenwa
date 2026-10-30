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

import { getFirstResponseTimeColor } from '@/features/usage-logs/lib/format'
import { formatNumber } from '@/lib/format'

import { formatDuration, formatTimestamp } from '../lib/format'
import type { ChannelHistory } from '../types'
import { HistoryBars, type HistoryBar } from './history-bars'

export function RouteHistory(props: {
  history?: ChannelHistory
  locale: string | undefined
}) {
  const { t } = useTranslation()
  const history = props.history
  const observations = (history?.observations ?? []).slice(0, 60).reverse()
  const realSamples = (history?.real_samples ?? []).slice(0, 30).reverse()
  const firstTokenBars: HistoryBar[] = Array.from(
    { length: 30 - realSamples.length },
    (_, index) => ({
      key: `empty-${index}`,
      label: t('No observation'),
      status: 'no_data',
      height: 'empty',
    })
  )
  const statusBars: HistoryBar[] = Array.from(
    { length: 60 - observations.length },
    (_, index) => ({
      key: `empty-${index}`,
      label: t('No observation'),
      status: 'no_data',
      height: 'empty',
    })
  )
  const labels = {
    healthy: t('Healthy'),
    degraded: t('High latency'),
    error: t('Error'),
  }
  for (const sample of realSamples) {
    let status: HistoryBar['status'] = 'no_data'
    let height: HistoryBar['height'] = 'empty'
    if (sample.status === 'error') {
      status = 'error'
      height = 'low'
    } else if (sample.ttft_ms != null && sample.ttft_ms > 0) {
      status = 'healthy'
      height = 'high'
      if (getFirstResponseTimeColor(sample.ttft_ms / 1000) === 'danger') {
        status = 'degraded'
        height = 'medium'
      }
    }
    firstTokenBars.push({
      key: sample.id,
      status,
      height,
      label: `${formatTimestamp(sample.observed_at, props.locale)}\n${t('Real request')} · ${labels[sample.status]}\n${t('First token')}: ${formatDuration(sample.ttft_ms)}\n${t('Total time')}: ${formatDuration(sample.latency_ms)}`,
    })
  }
  for (const sample of observations) {
    let height: HistoryBar['height'] = 'high'
    if (sample.status === 'degraded') height = 'medium'
    if (sample.status === 'error') height = 'low'
    statusBars.push({
      key: sample.id,
      status: sample.status,
      height,
      label: `${formatTimestamp(sample.observed_at, props.locale)}\n${sample.source === 'real' ? t('Real request') : t('Probe')} · ${labels[sample.status]}\n${t('First token')}: ${formatDuration(sample.ttft_ms)}\n${t('Total time')}: ${formatDuration(sample.latency_ms)}`,
    })
  }
  const count = observations.length
  const successCount = observations.filter(
    (sample) => sample.status !== 'error'
  ).length
  const successRate = count > 0 ? (successCount / count) * 100 : undefined
  return (
    <div className='space-y-4 border-t pt-3'>
      <section
        className='space-y-2'
        aria-label={t('Real request first-token history')}
      >
        <div className='text-muted-foreground flex flex-wrap items-center justify-between gap-1 text-[11px]'>
          <h5 className='font-medium'>
            {t('Real request first-token history')}
          </h5>
          <span>{t('Latest 30 real requests')}</span>
        </div>
        <HistoryBars
          label={t('Real request first-token history')}
          bars={firstTokenBars}
        />
      </section>
      <section className='space-y-2' aria-label={t('Status history')}>
        <div className='flex flex-wrap items-end justify-between gap-2'>
          <div className='min-w-0'>
            <h5 className='text-muted-foreground text-[11px] font-medium'>
              {t('Observed success rate')}
            </h5>
            <p className='text-muted-foreground mt-0.5 text-[10px]'>
              {t('{{count}} observations', { count })}
            </p>
          </div>
          <strong className='font-mono text-xl font-semibold tabular-nums'>
            {successRate == null
              ? '—'
              : `${formatNumber(successRate, props.locale)}%`}
          </strong>
        </div>
        <HistoryBars label={t('Status history')} bars={statusBars} />
        <div className='text-muted-foreground flex items-center justify-between gap-2 text-[10px]'>
          <span>{t('Latest 60 observations')}</span>
          <span>{t('Current')}</span>
        </div>
      </section>
      {!history && (
        <p className='text-muted-foreground text-[10px]'>
          {t('History collection has not started.')}
        </p>
      )}
      {history && count === 0 && (
        <p className='text-muted-foreground text-[10px]'>
          {t('No observations yet.')}
        </p>
      )}
      {history && count > 0 && (
        <p className='text-muted-foreground text-[10px]'>
          {t('Gray placeholders indicate no recorded observation.')}
        </p>
      )}
    </div>
  )
}
