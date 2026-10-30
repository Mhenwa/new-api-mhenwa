import { z } from 'zod'

import type {
  ChannelStatusMonitorData,
  ChannelStatusMonitorTarget,
  ChannelStatusProbeToken,
} from '../admin-api'

const targetSchema = z.object({
  group: z.string(),
  model: z.string(),
  probe_token_id: z.number(),
  interval_seconds: z.number().optional().default(300),
  timeout_seconds: z.number().optional().default(30),
})

// Missing or malformed data must not become an editable empty list. The server
// legitimately returns null targets when nothing has been configured.
export const monitorDataSchema = z.object({
  targets: z
    .array(targetSchema)
    .nullable()
    .transform((targets) => targets ?? []),
  source: z.string().optional(),
  groups: z.array(z.string()),
  models_by_group: z.record(z.string(), z.array(z.string())),
  probe_tokens: z.array(
    z.object({
      id: z.number().int().positive(),
      name: z.string().optional(),
      user_id: z.number().optional(),
      group: z.string(),
      models: z.array(z.string()).optional(),
    })
  ),
})

export function eligibleProbeTokens(
  tokens: ChannelStatusProbeToken[],
  target: Pick<ChannelStatusMonitorTarget, 'group' | 'model'>
): ChannelStatusProbeToken[] {
  return tokens.filter(
    (token) =>
      token.group === target.group &&
      Boolean(token.models?.includes(target.model))
  )
}

export function createMonitorFormSchema(
  data: ChannelStatusMonitorData | undefined,
  t: (key: string) => string
) {
  return z
    .object({
      targets: z
        .array(
          z.object({
            group: z.string().min(1, t('Select a valid group')),
            model: z.string().min(1, t('Select a valid model for this group')),
            probe_token_id: z
              .number()
              .int()
              .positive(t('Select an eligible probe key')),
            interval_seconds: z
              .number()
              .int(t('Enter a whole number of seconds'))
              .min(60, t('Interval must be between 60 and 3600 seconds'))
              .max(3600, t('Interval must be between 60 and 3600 seconds')),
            timeout_seconds: z
              .number()
              .int(t('Enter a whole number of seconds'))
              .min(5, t('Timeout must be between 5 and 120 seconds'))
              .max(120, t('Timeout must be between 5 and 120 seconds')),
          })
        )
        .max(100, t('You can configure at most 100 monitored routes')),
    })
    .superRefine((value, context) => {
      const routes = new Set<string>()
      const tokens = new Set<number>()
      value.targets.forEach((target, index) => {
        const addIssue = (
          field: keyof ChannelStatusMonitorTarget,
          message: string
        ) =>
          context.addIssue({
            code: 'custom',
            path: ['targets', index, field],
            message,
          })
        if (!data?.groups.includes(target.group)) {
          addIssue('group', t('Select a valid group'))
        }
        if (!data?.models_by_group[target.group]?.includes(target.model)) {
          addIssue('model', t('Select a valid model for this group'))
        }
        if (
          !eligibleProbeTokens(data?.probe_tokens ?? [], target).some(
            (token) => token.id === target.probe_token_id
          )
        ) {
          addIssue('probe_token_id', t('Select an eligible probe key'))
        }
        const key = JSON.stringify([target.group, target.model])
        if (routes.has(key)) {
          addIssue('model', t('Each group and model route must be unique'))
        }
        if (tokens.has(target.probe_token_id) && target.probe_token_id > 0) {
          addIssue(
            'probe_token_id',
            t('Each probe key can only be used by one route')
          )
        }
        routes.add(key)
        tokens.add(target.probe_token_id)
      })
    })
}

export type MonitorFormValues = z.infer<
  ReturnType<typeof createMonitorFormSchema>
>
