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
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

export interface HistoryBar {
  key: string
  label: string
  status: 'healthy' | 'degraded' | 'error' | 'mixed' | 'no_data'
  height: 'high' | 'medium' | 'low' | 'empty'
}

/** Shared renderer for real first-token samples and completed observations. */
export function HistoryBars(props: { label: string; bars: HistoryBar[] }) {
  const { t } = useTranslation()
  const [focused, setFocused] = useState(0)
  const buttons = useRef<(HTMLButtonElement | null)[]>([])
  return (
    <div
      className='flex h-8 w-full min-w-0 items-end gap-0.5'
      role='group'
      aria-label={props.label}
    >
      {props.bars.map((bar, index) => (
        <Tooltip key={bar.key}>
          <TooltipTrigger
            render={
              <button
                type='button'
                data-status={bar.status}
                ref={(element) => {
                  buttons.current[index] = element
                }}
                tabIndex={
                  index === Math.min(focused, props.bars.length - 1) ? 0 : -1
                }
                onFocus={() => setFocused(index)}
                onKeyDown={(event) => {
                  let next = index
                  if (event.key === 'ArrowRight') {
                    next = Math.min(index + 1, props.bars.length - 1)
                  } else if (event.key === 'ArrowLeft') {
                    next = Math.max(index - 1, 0)
                  } else if (event.key === 'Home') next = 0
                  else if (event.key === 'End') next = props.bars.length - 1
                  else return
                  event.preventDefault()
                  setFocused(next)
                  buttons.current[next]?.focus()
                }}
                className={cn(
                  'focus-visible:ring-ring min-w-0 flex-1 rounded-sm outline-offset-2 transition-opacity hover:opacity-75 focus-visible:ring-2',
                  bar.status === 'healthy' &&
                    'bg-emerald-500 dark:bg-emerald-400',
                  bar.status === 'mixed' && 'bg-amber-400 dark:bg-amber-300',
                  bar.status === 'degraded' && 'bg-amber-400 dark:bg-amber-300',
                  bar.status === 'error' && 'bg-red-500 dark:bg-red-400',
                  bar.status === 'no_data' && 'bg-muted-foreground/25',
                  bar.height === 'high' && 'h-full',
                  bar.height === 'medium' && 'h-3/4',
                  bar.height === 'low' && 'h-2/5',
                  bar.height === 'empty' && 'h-1/5'
                )}
                aria-label={bar.label || t('No observation')}
              />
            }
          />
          <TooltipContent
            role='tooltip'
            className='max-w-64 whitespace-pre-line'
          >
            {bar.label || t('No observation')}
          </TooltipContent>
        </Tooltip>
      ))}
    </div>
  )
}
