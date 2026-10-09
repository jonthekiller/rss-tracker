import {
  ActionRowBuilder,
  AttachmentBuilder,
  type AutocompleteInteraction,
  type ChatInputCommandInteraction,
  type Client,
  type Interaction,
  MessageFlags,
  ModalBuilder,
  type ModalSubmitInteraction,
  PermissionFlagsBits,
  REST,
  Routes,
  SlashCommandBuilder,
  TextInputBuilder,
  TextInputStyle,
} from 'discord.js'
import {
  exportBackup,
  newFeedId,
  type RestoreEntryResult,
  restoreBackup,
} from '../backup/export.js'
import type { BatchMode, Feed, FilterField, FilterKind } from '../db/feeds.js'
import { fetchFeed } from '../feed/fetcher.js'
import type { Article } from '../feed/types.js'
import { importMonitoRssBackup } from '../import/monitorss.js'
import { buildArticlePayload } from '../render/embed.js'
import { DEFAULT_TEMPLATE } from '../render/template.js'
import type { DryRunReport } from '../scheduler/scheduler.js'
import type { BotContext } from './context.js'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

async function reply(interaction: ChatInputCommandInteraction, content: string, ephemeral = true) {
  if (interaction.deferred || interaction.replied) {
    return interaction.editReply({ content })
  }
  return interaction.reply({ content, flags: ephemeral ? MessageFlags.Ephemeral : undefined })
}

function _requireManageGuild(interaction: ChatInputCommandInteraction): boolean {
  return (
    interaction.inGuild() &&
    (interaction.memberPermissions?.has(PermissionFlagsBits.ManageGuild) ?? false)
  )
}

function isAdmin(
  interaction: ChatInputCommandInteraction | ModalSubmitInteraction | AutocompleteInteraction,
): boolean {
  return (
    interaction.inGuild() &&
    (interaction.memberPermissions?.has(PermissionFlagsBits.ManageGuild) ?? false)
  )
}

/**
 * Channel-scoped permission model: a user may configure the feeds of a
 * channel if they can WRITE in that channel (view + send). Server
 * administrators bypass the check. This keeps configuration rights tied to
 * the channel itself instead of a server-wide role.
 */
async function canManageChannel(
  interaction: ChatInputCommandInteraction | ModalSubmitInteraction,
  channelId: string,
): Promise<boolean> {
  if (!interaction.inGuild()) return false
  if (isAdmin(interaction)) return true
  const guild = interaction.guild
  if (!guild) return false
  const channel = guild.channels.cache.get(channelId)
  const member = guild.members.cache.get(interaction.user.id)
  if (!channel || !member) return false
  const perms = channel.permissionsFor(member)
  return perms.has(PermissionFlagsBits.ViewChannel) && perms.has(PermissionFlagsBits.SendMessages)
}

async function denyChannelAccess(
  interaction: ChatInputCommandInteraction,
  channelId: string,
): Promise<void> {
  await reply(
    interaction,
    `🔒 You need the **Send Messages** permission in <#${channelId}> to configure its feeds. (Server administrators can always configure.)`,
  )
}

function channelExists(context: BotContext, guildId: string, channelId: string): Promise<boolean> {
  return Promise.resolve(
    context.client.guilds.cache.get(guildId)?.channels.cache.has(channelId) ?? false,
  )
}

function formatFeedLine(feed: Feed): string {
  const status = !feed.enabled ? '⏸️' : feed.consecutiveFailures > 0 ? '⚠️' : '✅'
  return `${status} **${feed.name}** — \`${feed.id.slice(0, 8)}\` → <#${feed.channelId}> (every ${feed.intervalMinutes} min)`
}

function chunkLines(lines: string[], maxChars = 1900): string[] {
  const chunks: string[] = []
  let current: string[] = []
  let length = 0
  for (const line of lines) {
    if (length + line.length + 1 > maxChars && current.length > 0) {
      chunks.push(current.join('\n'))
      current = []
      length = 0
    }
    current.push(line)
    length += line.length + 1
  }
  if (current.length > 0) chunks.push(current.join('\n'))
  return chunks
}

function formatResultsReport(
  title: string,
  results: { name: string; url: string; status: string; detail?: string | undefined }[],
): string {
  const lines = results.map((r) => {
    const icon =
      r.status === 'error' || r.status === 'channel_missing' ? '❌' : r.detail ? '⚠️' : '✅'
    return `${icon} **${r.name}** — ${r.status}${r.detail ? ` (${r.detail})` : ''}`
  })
  const summary = chunkLines([`**${title}**`, ...lines])
  return summary[0] ?? title
}

async function getAttachmentJson(
  interaction: ChatInputCommandInteraction,
  name: string,
): Promise<unknown> {
  const attachment = interaction.options.getAttachment(name, true)
  if (!attachment.name?.endsWith('.json')) {
    throw new Error('Please attach a .json file')
  }
  const response = await fetch(attachment.url)
  if (!response.ok) {
    throw new Error(`Could not download the attachment (HTTP ${response.status})`)
  }
  const text = await response.text()
  if (text.length > 8 * 1024 * 1024) {
    throw new Error('File too large (max 8 MB)')
  }
  return JSON.parse(text)
}

// ---------------------------------------------------------------------------
// Command registry
// ---------------------------------------------------------------------------

interface CommandHandler {
  data: SlashCommandBuilder
  handle: (context: BotContext, interaction: ChatInputCommandInteraction) => Promise<unknown>
}

export function buildCommandPayloads() {
  return commands.map((c) => c.data.toJSON())
}

export async function registerCommands(client: Client): Promise<void> {
  if (!client.user) throw new Error('Client not ready')
  const rest = new REST().setToken(client.token ?? '')
  // Global registration + per-guild registration (guild commands are
  // available instantly instead of after Discord's global cache refresh).
  await rest.put(Routes.applicationCommands(client.user.id), { body: buildCommandPayloads() })
  for (const guild of client.guilds.cache.values()) {
    await rest.put(Routes.applicationGuildCommands(client.user.id, guild.id), {
      body: buildCommandPayloads(),
    })
  }
}

export async function handleInteraction(
  context: BotContext,
  interaction: Interaction,
): Promise<void> {
  if (interaction.isAutocomplete()) {
    await handleAutocomplete(context, interaction)
    return
  }
  if (interaction.isModalSubmit()) {
    await handleModalSubmit(context, interaction)
    return
  }
  if (!interaction.isChatInputCommand()) return

  const command = commands.find((c) => c.data.name === interaction.commandName)
  if (!command) {
    await interaction.reply({ content: 'Unknown command.', flags: MessageFlags.Ephemeral })
    return
  }
  // Every command enforces its own scope: feed commands require write access
  // to the channel they touch, settings/import/backup operate on the current
  // channel, and server-wide options (backup all_channels) check Manage Server.
  await command.handle(context, interaction)
}

// ---------------------------------------------------------------------------
// Autocomplete (feed IDs)
// ---------------------------------------------------------------------------

async function handleAutocomplete(
  context: BotContext,
  interaction: AutocompleteInteraction,
): Promise<void> {
  const focused = interaction.options.getFocused(true)
  if (focused.name !== 'id') {
    await interaction.respond([])
    return
  }
  const guild = interaction.guild
  const feeds = context.store.getFeedsForGuild(interaction.guildId ?? '')
  // Only suggest feeds of channels the user can manage (write access or admin).
  const member = guild?.members.cache.get(interaction.user.id)
  const manageable = feeds.filter((f) => {
    if (f.guildId !== interaction.guildId) return false
    if (isAdmin(interaction)) return true
    const channel = guild?.channels.cache.get(f.channelId)
    if (!channel || !member) return false
    const perms = channel.permissionsFor(member)
    return perms.has(PermissionFlagsBits.ViewChannel) && perms.has(PermissionFlagsBits.SendMessages)
  })
  const query = focused.value.toLowerCase()
  const choices = manageable
    .filter((f) => f.name.toLowerCase().includes(query) || f.id.startsWith(query))
    .slice(0, 25)
    .map((f) => ({ name: `${f.name} (${f.id.slice(0, 8)})`, value: f.id }))
  await interaction.respond(choices)
}

// ---------------------------------------------------------------------------
// /feed
// ---------------------------------------------------------------------------

const feedCommand: CommandHandler = {
  data: new SlashCommandBuilder()
    .setName('feed')
    .setDescription('Manage RSS feeds')
    .setDMPermission(false)
    .addSubcommand((sub) =>
      sub
        .setName('add')
        .setDescription('Add a feed to a channel')
        .addStringOption((o) => o.setName('url').setDescription('Feed URL').setRequired(true))
        .addChannelOption((o) =>
          o.setName('channel').setDescription('Target channel (default: current channel)'),
        )
        .addStringOption((o) => o.setName('name').setDescription('Feed display name')),
    )
    .addSubcommand((sub) =>
      sub
        .setName('list')
        .setDescription('List feeds (of a channel or the whole server)')
        .addChannelOption((o) => o.setName('channel').setDescription('Only this channel')),
    )
    .addSubcommand((sub) =>
      sub
        .setName('remove')
        .setDescription('Remove a feed')
        .addStringOption((o) => feedIdOption(o)),
    )
    .addSubcommand((sub) =>
      sub
        .setName('show')
        .setDescription('Show a feed configuration')
        .addStringOption((o) => feedIdOption(o)),
    )
    .addSubcommand((sub) =>
      sub
        .setName('pause')
        .setDescription('Pause a feed')
        .addStringOption((o) => feedIdOption(o)),
    )
    .addSubcommand((sub) =>
      sub
        .setName('resume')
        .setDescription('Resume a feed')
        .addStringOption((o) => feedIdOption(o)),
    )
    .addSubcommand((sub) =>
      sub
        .setName('move')
        .setDescription('Move a feed to another channel')
        .addStringOption((o) => feedIdOption(o))
        .addChannelOption((o) =>
          o.setName('channel').setDescription('New channel').setRequired(true),
        ),
    )
    .addSubcommand((sub) =>
      sub
        .setName('template')
        .setDescription('Edit the message template')
        .addStringOption((o) => feedIdOption(o)),
    )
    .addSubcommand((sub) =>
      sub
        .setName('embed')
        .setDescription('Edit the embed template (JSON)')
        .addStringOption((o) => feedIdOption(o)),
    )
    .addSubcommand((sub) =>
      sub
        .setName('frequency')
        .setDescription('Check frequency in minutes')
        .addStringOption((o) => feedIdOption(o))
        .addIntegerOption((o) =>
          o
            .setName('minutes')
            .setDescription('Interval in minutes (min 2)')
            .setMinValue(2)
            .setMaxValue(1440)
            .setRequired(true),
        ),
    )
    .addSubcommand((sub) =>
      sub
        .setName('batch')
        .setDescription('Burst handling mode')
        .addStringOption((o) => feedIdOption(o))
        .addStringOption((o) =>
          o
            .setName('mode')
            .setDescription('auto: digest above threshold')
            .setRequired(true)
            .addChoices(
              { name: 'auto', value: 'auto' },
              { name: 'individual', value: 'individual' },
              { name: 'digest', value: 'digest' },
            ),
        )
        .addIntegerOption((o) =>
          o.setName('threshold').setDescription('Digest threshold (auto mode)').setMinValue(2),
        )
        .addIntegerOption((o) =>
          o.setName('max_items').setDescription('Max items listed in a digest').setMinValue(1),
        ),
    )
    .addSubcommandGroup((group) =>
      group
        .setName('filter')
        .setDescription('Manage content filters')
        .addSubcommand((sub) =>
          sub
            .setName('add')
            .setDescription('Add a filter')
            .addStringOption((o) => feedIdOption(o))
            .addStringOption((o) =>
              o
                .setName('kind')
                .setDescription('include: only matching articles; exclude: drop matching articles')
                .setRequired(true)
                .addChoices(
                  { name: 'include', value: 'include' },
                  { name: 'exclude', value: 'exclude' },
                ),
            )
            .addStringOption((o) =>
              o
                .setName('field')
                .setDescription('Field to match')
                .setRequired(true)
                .addChoices(
                  { name: 'title', value: 'title' },
                  { name: 'description', value: 'description' },
                  { name: 'any', value: 'any' },
                ),
            )
            .addStringOption((o) =>
              o.setName('text').setDescription('Keyword or regex').setRequired(true),
            )
            .addBooleanOption((o) =>
              o.setName('regex').setDescription('Treat the text as a regex'),
            ),
        )
        .addSubcommand((sub) =>
          sub
            .setName('remove')
            .setDescription('Remove a filter by id (see /feed show)')
            .addStringOption((o) => feedIdOption(o))
            .addIntegerOption((o) =>
              o.setName('filter_id').setDescription('Filter id').setRequired(true),
            ),
        ),
    )
    .addSubcommand((sub) =>
      sub
        .setName('test')
        .setDescription('Simulate a send with the current template (no state change)')
        .addStringOption((o) => feedIdOption(o))
        .addIntegerOption((o) =>
          o.setName('n').setDescription('Number of articles (1-5)').setMinValue(1).setMaxValue(5),
        )
        .addStringOption((o) =>
          o
            .setName('mode')
            .setDescription('preview: ephemeral only; send: real message in this channel')
            .addChoices(
              { name: 'preview (ephemeral)', value: 'preview' },
              { name: 'send to this channel', value: 'send' },
            ),
        ),
    )
    .addSubcommand((sub) =>
      sub
        .setName('refresh')
        .setDescription('Check a feed immediately')
        .addStringOption((o) => feedIdOption(o)),
    ) as SlashCommandBuilder,

  async handle(context, interaction) {
    const group = interaction.options.getSubcommandGroup(false)
    const sub = interaction.options.getSubcommand(true)
    if (group === 'filter') {
      if (sub === 'add') await feedFilterAdd(context, interaction)
      else await feedFilterRemove(context, interaction)
      return
    }
    switch (sub) {
      case 'add':
        await feedAdd(context, interaction)
        return
      case 'list':
        await feedList(context, interaction)
        return
      case 'remove':
        await feedRemove(context, interaction)
        return
      case 'show':
        await feedShow(context, interaction)
        return
      case 'pause':
        await feedPauseResume(context, interaction, false)
        return
      case 'resume':
        await feedPauseResume(context, interaction, true)
        return
      case 'move':
        await feedMove(context, interaction)
        return
      case 'template':
        await feedTemplateModal(context, interaction)
        return
      case 'embed':
        await feedEmbedModal(context, interaction)
        return
      case 'frequency':
        await feedFrequency(context, interaction)
        return
      case 'batch':
        await feedBatch(context, interaction)
        return
      case 'test':
        await feedTest(context, interaction)
        return
      case 'refresh':
        await feedRefresh(context, interaction)
        return
      default:
        await reply(interaction, 'Unknown subcommand.')
    }
  },
}

function feedIdOption<
  T extends {
    setName: (n: string) => T
    setDescription: (d: string) => T
    setRequired: (r: boolean) => T
    setAutocomplete: (a: boolean) => T
  },
>(option: T): T {
  return option
    .setName('id')
    .setDescription('Feed id (autocomplete)')
    .setRequired(true)
    .setAutocomplete(true)
}

async function getFeedOrReply(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<Feed | null> {
  const id = interaction.options.getString('id', true)
  const feed = context.store.getFeed(id)
  if (!feed) {
    await reply(interaction, `Feed \`${id}\` not found. Use \`/feed list\` to see ids.`)
    return null
  }
  // The feed must belong to this server, and the user must be allowed to
  // manage the channel it posts into (write access or server admin).
  if (feed.guildId !== interaction.guildId) {
    await reply(interaction, `Feed \`${id}\` not found. Use \`/feed list\` to see ids.`)
    return null
  }
  if (!(await canManageChannel(interaction, feed.channelId))) {
    await denyChannelAccess(interaction, feed.channelId)
    return null
  }
  return feed
}

async function feedAdd(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  await interaction.deferReply({ flags: MessageFlags.Ephemeral })
  const url = interaction.options.getString('url', true).trim()
  const channel = interaction.options.getChannel('channel')
  const channelId = channel?.id ?? interaction.channelId
  const name = interaction.options.getString('name')?.trim() || 'Unknown'

  // You can only add feeds to a channel you can write in.
  if (!(await canManageChannel(interaction, channelId))) {
    await denyChannelAccess(interaction, channelId)
    return
  }

  try {
    new URL(url)
  } catch {
    await interaction.editReply(`Invalid URL: \`${url}\``)
    return
  }

  if (context.store.exists(url, channelId)) {
    await interaction.editReply('This feed already exists for this channel.')
    return
  }

  let feedName = name
  let articleCount = 0
  let warning = ''
  try {
    const result = await fetchFeed(url)
    if (result.status === 'ok') {
      if (name === 'Unknown' && result.meta.title) feedName = result.meta.title
      articleCount = result.articles.length
    }
  } catch (error) {
    warning = `⚠️ Could not fetch the feed right now (${error instanceof Error ? error.message : String(error)}); it will retry automatically.`
  }

  const feed = context.store.addFeed({
    id: newFeedId(),
    name: feedName,
    url,
    guildId: interaction.guildId ?? '',
    channelId,
    templateText: DEFAULT_TEMPLATE,
    intervalMinutes: context.config.defaultIntervalMinutes,
  })

  // Baseline: mark current articles as seen without sending.
  try {
    const result = await fetchFeed(url)
    if (result.status === 'ok') {
      context.pipeline.baseline(feed, result.articles, Date.now())
      articleCount = result.articles.length
    }
  } catch {
    // Keep the warning from the first attempt.
  }

  await interaction.editReply(
    `✅ Feed **${feedName}** added to <#${channelId}> (\`${feed.id.slice(0, 8)}\`).\n` +
      `${articleCount > 0 ? `${articleCount} existing article(s) marked as seen (nothing will be sent for those).` : ''}\n` +
      warning,
  )
}

async function feedList(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const channel = interaction.options.getChannel('channel')
  let feeds: Feed[]
  if (channel) {
    // Listing another channel's feeds requires write access there.
    if (!(await canManageChannel(interaction, channel.id))) {
      await denyChannelAccess(interaction, channel.id)
      return
    }
    feeds = context.store.getFeedsForChannel(channel.id)
  } else if (isAdmin(interaction)) {
    feeds = context.store.getFeedsForGuild(interaction.guildId ?? '')
  } else {
    // Non-admins only see the feeds of the channel they are typing in.
    feeds = context.store.getFeedsForChannel(interaction.channelId)
  }
  if (feeds.length === 0) {
    await reply(interaction, 'No feeds found. Add one with `/feed add`.')
    return
  }
  const lines = feeds.map(formatFeedLine)
  const chunks = chunkLines([`**Feeds (${feeds.length})**`, ...lines])
  await reply(interaction, chunks[0] ?? 'Nothing to display.')
  for (const chunk of chunks.slice(1, 5)) {
    await interaction.followUp({ content: chunk, flags: MessageFlags.Ephemeral })
  }
}

async function feedRemove(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  context.store.deleteFeed(feed.id)
  await reply(interaction, `🗑️ Feed **${feed.name}** removed.`)
}

async function feedShow(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const filters = context.store.getFilters(feed.id)
  const filterLines = filters.map(
    (f) => `  - \`#${f.id}\` ${f.kind} ${f.field}${f.isRegex ? ' (regex)' : ''}: \`${f.pattern}\``,
  )
  const lines = [
    `**${feed.name}** (\`${feed.id}\`)`,
    `URL: <${feed.url}>`,
    `Channel: <#${feed.channelId}>`,
    `Enabled: ${feed.enabled}${feed.pauseReason ? ` (${feed.pauseReason})` : ''}`,
    `Frequency: every ${feed.intervalMinutes} min`,
    `Batch: ${feed.batchMode} (threshold ${feed.batchThreshold}, max ${feed.batchMaxItems} items)`,
    `Max article age: ${feed.maxAgeDays === 0 ? 'disabled' : `${feed.maxAgeDays} days`}`,
    `Template:`,
    '```',
    feed.templateText,
    '```',
  ]
  if (feed.embedJson) {
    lines.push('Embed:', '```json', feed.embedJson.slice(0, 500), '```')
  }
  if (feed.lastError) {
    lines.push(`Last error (${feed.consecutiveFailures} consecutive): ${feed.lastError}`)
  }
  lines.push(`Seen articles: ${context.store.countSeen(feed.id)}`)
  if (filterLines.length > 0) lines.push('Filters:', ...filterLines)

  const chunks = chunkLines(lines)
  await reply(interaction, chunks[0] ?? 'Nothing to display.')
  for (const chunk of chunks.slice(1, 5)) {
    await interaction.followUp({ content: chunk, flags: MessageFlags.Ephemeral })
  }
}

async function feedPauseResume(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
  resume: boolean,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  context.store.updateFeed(
    feed.id,
    resume
      ? { enabled: true, pauseReason: null, nextCheckAt: 0 }
      : { enabled: false, pauseReason: 'manual' },
  )
  await reply(
    interaction,
    resume ? `▶️ Feed **${feed.name}** resumed.` : `⏸️ Feed **${feed.name}** paused.`,
  )
}

async function feedMove(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const channel = interaction.options.getChannel('channel', true)
  const resolved = interaction.guild?.channels.cache.get(channel.id)
  if (!resolved?.isTextBased() || !resolved.isSendable()) {
    await reply(interaction, 'The target must be a text channel I can write to.')
    return
  }
  // Moving requires write access on the destination channel too.
  if (!(await canManageChannel(interaction, channel.id))) {
    await denyChannelAccess(interaction, channel.id)
    return
  }
  context.store.updateFeed(feed.id, { channelId: channel.id, pauseReason: null })
  await reply(interaction, `📦 Feed **${feed.name}** moved to <#${channel.id}>.`)
}

async function feedTemplateModal(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const modal = new ModalBuilder()
    .setCustomId(`feed-template:${feed.id}`)
    .setTitle(`Template — ${feed.name.slice(0, 40)}`)
    .addComponents(
      new ActionRowBuilder<TextInputBuilder>().addComponents(
        new TextInputBuilder()
          .setCustomId('template')
          .setLabel('Placeholders: {title} {link} {description}…')
          .setStyle(TextInputStyle.Paragraph)
          .setValue(feed.templateText)
          .setRequired(true),
      ),
    )
  await interaction.showModal(modal)
}

async function feedEmbedModal(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const modal = new ModalBuilder()
    .setCustomId(`feed-embed:${feed.id}`)
    .setTitle(`Embed JSON — ${feed.name.slice(0, 40)}`)
    .addComponents(
      new ActionRowBuilder<TextInputBuilder>().addComponents(
        new TextInputBuilder()
          .setCustomId('embed')
          .setLabel('Embed template JSON (empty = none)')
          .setStyle(TextInputStyle.Paragraph)
          .setValue(feed.embedJson ?? ''),
      ),
    )
  await interaction.showModal(modal)
}

async function feedFrequency(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const minutes = interaction.options.getInteger('minutes', true)
  context.store.updateFeed(feed.id, { intervalMinutes: minutes })
  await reply(interaction, `⏱️ Feed **${feed.name}** will now be checked every ${minutes} min.`)
}

async function feedBatch(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const mode = interaction.options.getString('mode', true) as BatchMode
  const threshold = interaction.options.getInteger('threshold')
  const maxItems = interaction.options.getInteger('max_items')
  context.store.updateFeed(feed.id, {
    batchMode: mode,
    ...(threshold !== null ? { batchThreshold: threshold } : {}),
    ...(maxItems !== null ? { batchMaxItems: maxItems } : {}),
  })
  await reply(interaction, `📚 Batch mode of **${feed.name}** set to \`${mode}\`.`)
}

async function feedFilterAdd(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const kind = interaction.options.getString('kind', true) as FilterKind
  const field = interaction.options.getString('field', true) as FilterField
  const text = interaction.options.getString('text', true)
  const isRegex = interaction.options.getBoolean('regex') ?? false
  if (isRegex) {
    try {
      new RegExp(text)
    } catch (error) {
      await reply(
        interaction,
        `Invalid regex: ${error instanceof Error ? error.message : String(error)}`,
      )
      return
    }
  }
  context.store.addFilter(feed.id, kind, field, text, isRegex)
  await reply(
    interaction,
    `🔎 Filter added on **${feed.name}**: ${kind} ${field}${isRegex ? ' (regex)' : ''}: \`${text}\``,
  )
}

async function feedFilterRemove(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const filterId = interaction.options.getInteger('filter_id', true)
  const removed = context.store.removeFilter(feed.id, filterId)
  await reply(
    interaction,
    removed
      ? `🗑️ Filter #${filterId} removed.`
      : `Filter #${filterId} not found on **${feed.name}**.`,
  )
}

async function feedTest(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  await interaction.deferReply({ flags: MessageFlags.Ephemeral })
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const n = interaction.options.getInteger('n') ?? 1
  const mode = interaction.options.getString('mode') ?? 'preview'
  // Real sends are capped lower to avoid flooding the channel.
  const count = mode === 'send' ? Math.min(n, 3) : n

  let articles: Article[]
  try {
    const result = await fetchFeed(feed.url)
    if (result.status === 'not_modified') {
      await interaction.editReply(
        'Feed unchanged since last check (HTTP 304) — nothing to preview. Use `/debug feed` for diagnostics.',
      )
      return
    }
    articles = result.articles
  } catch (error) {
    await interaction.editReply(
      `Failed to fetch feed: ${error instanceof Error ? error.message : String(error)}`,
    )
    return
  }

  const latest = articles.slice(-count).reverse()
  let sent = 0
  for (const article of latest) {
    const payload = buildArticlePayload(feed.templateText, feed.embedJson, {
      article,
      feedName: feed.name,
      timezone: context.config.timezone,
    })
    const embeds = payload.embeds.length > 0 ? payload.embeds : []
    if (mode === 'send') {
      const channel = interaction.channel
      if (!channel?.isSendable()) {
        await interaction.editReply('Cannot send in this channel.')
        return
      }
      await channel.send({ content: payload.content, embeds })
      sent += 1
    } else {
      await interaction.followUp({
        content: payload.content,
        embeds,
        flags: MessageFlags.Ephemeral,
      })
    }
  }

  const summary =
    mode === 'send'
      ? `✅ Simulated ${sent} send(s) to this channel with the current template. **State unchanged** (no article marked seen).`
      : `👀 Preview of ${latest.length} article(s) (ephemeral). Use mode \`send\` to post a real message in this channel.`
  await interaction.editReply(summary)
}

async function feedRefresh(
  context: BotContext,
  interaction: ChatInputCommandInteraction,
): Promise<void> {
  await interaction.deferReply({ flags: MessageFlags.Ephemeral })
  const feed = await getFeedOrReply(context, interaction)
  if (!feed) return
  const outcome = await context.pipeline.checkFeed(feed.id)
  await interaction.editReply(
    outcome.status === 'error'
      ? `❌ Check failed: ${outcome.error}`
      : outcome.status === 'delivered'
        ? `✅ Checked — ${outcome.freshCount} new article(s) sent.`
        : '✅ Checked — nothing new.',
  )
}

// ---------------------------------------------------------------------------
// /import
// ---------------------------------------------------------------------------

const importCommand: CommandHandler = {
  data: new SlashCommandBuilder()
    .setName('import')
    .setDescription('Import data from an external bot')
    .setDMPermission(false)
    .addSubcommand((sub) =>
      sub
        .setName('monitorss')
        .setDescription('Import a MonitoRSS backup (JSON attachment)')
        .addAttachmentOption((o) =>
          o.setName('file').setDescription('MonitoRSS backup .json').setRequired(true),
        )
        .addChannelOption((o) =>
          o.setName('channel').setDescription('Fallback destination channel'),
        ),
    ) as SlashCommandBuilder,

  async handle(context, interaction) {
    if (interaction.options.getSubcommand(true) !== 'monitorss') {
      await reply(interaction, 'Unknown subcommand.')
      return
    }
    await interaction.deferReply({ flags: MessageFlags.Ephemeral })

    const guildId = interaction.guildId ?? ''
    const fallbackChannelId = interaction.options.getChannel('channel')?.id ?? interaction.channelId

    // Import is channel-scoped: the importer must be able to write in the
    // destination channel (explicit option or the current channel).
    if (!(await canManageChannel(interaction, fallbackChannelId))) {
      await interaction.editReply(
        `🔒 You need the **Send Messages** permission in <#${fallbackChannelId}> to import feeds there.`,
      )
      return
    }

    let data: unknown
    try {
      data = await getAttachmentJson(interaction, 'file')
    } catch (error) {
      await interaction.editReply(`❌ ${error instanceof Error ? error.message : String(error)}`)
      return
    }

    context.scheduler.suspend()
    try {
      const results = await importMonitoRssBackup(context.store, data, {
        guildId,
        fallbackChannelId,
        channelExists: (g, c) => channelExists(context, g, c),
        actorCanManageChannel: (c) => canManageChannel(interaction, c),
        defaultTemplate: DEFAULT_TEMPLATE,
      })
      await interaction.editReply(
        formatResultsReport(`MonitoRSS import — ${results.length} feed(s)`, results),
      )
    } finally {
      context.scheduler.resume()
    }
  },
}

// ---------------------------------------------------------------------------
// /backup
// ---------------------------------------------------------------------------

const backupCommand: CommandHandler = {
  data: new SlashCommandBuilder()
    .setName('backup')
    .setDescription('Backup and restore configuration')
    .setDMPermission(false)
    .addSubcommand((sub) =>
      sub
        .setName('export')
        .setDescription('Export configuration as JSON (this channel by default)')
        .addChannelOption((o) =>
          o.setName('channel').setDescription('Export this channel only (default: current)'),
        )
        .addBooleanOption((o) =>
          o.setName('all_channels').setDescription('Export every channel (requires Manage Server)'),
        ),
    )
    .addSubcommand((sub) =>
      sub
        .setName('restore')
        .setDescription('Restore from a backup JSON file')
        .addAttachmentOption((o) =>
          o.setName('file').setDescription('Backup .json file').setRequired(true),
        ),
    ) as SlashCommandBuilder,

  async handle(context, interaction) {
    const sub = interaction.options.getSubcommand(true)
    if (sub === 'export') {
      const all = interaction.options.getBoolean('all_channels') ?? false
      if (all && !isAdmin(interaction)) {
        await reply(
          interaction,
          'Exporting **all channels** requires the **Manage Server** permission. Export a single channel with `/backup export channel:<channel>`.',
        )
        return
      }
      let channelIds: string[] | undefined
      let scopeLabel = 'all channels'
      if (!all) {
        const channelId = interaction.options.getChannel('channel')?.id ?? interaction.channelId
        if (!(await canManageChannel(interaction, channelId))) {
          await denyChannelAccess(interaction, channelId)
          return
        }
        channelIds = [channelId]
        scopeLabel = `<#${channelId}>`
      }
      const backup = exportBackup(context.store, channelIds)
      const feedCount = backup.channels.reduce((sum, c) => sum + c.feeds.length, 0)
      const buffer = Buffer.from(JSON.stringify(backup, null, 2), 'utf-8')
      const file = new AttachmentBuilder(buffer, {
        name: `rrs-backup-${new Date().toISOString().slice(0, 10)}.json`,
      })
      await interaction.reply({
        content: `💾 Backup (${scopeLabel}): ${feedCount} feed(s) in ${backup.channels.length} channel(s), exported ${backup.exportedAt}`,
        files: [file],
        flags: MessageFlags.Ephemeral,
      })
      return
    }

    await interaction.deferReply({ flags: MessageFlags.Ephemeral })
    const guildId = interaction.guildId ?? ''
    let data: unknown
    try {
      data = await getAttachmentJson(interaction, 'file')
    } catch (error) {
      await interaction.editReply(`❌ ${error instanceof Error ? error.message : String(error)}`)
      return
    }

    context.scheduler.suspend()
    try {
      const results = await restoreBackup(context.store, data, {
        guildId,
        channelExists: (g, c) => channelExists(context, g, c),
        actorCanManageChannel: (c) => canManageChannel(interaction, c),
        baseline: async (feedId, url) => {
          try {
            const result = await fetchFeed(url)
            if (result.status === 'ok') {
              context.pipeline.baseline({ id: feedId }, result.articles, Date.now())
              return { ok: true, feedName: result.meta.title ?? undefined }
            }
            return { ok: true }
          } catch (error) {
            return { ok: false, error: error instanceof Error ? error.message : String(error) }
          }
        },
      })
      await interaction.editReply(
        formatResultsReport(`Restore — ${results.length} feed(s)`, results as RestoreEntryResult[]),
      )
    } finally {
      context.scheduler.resume()
    }
  },
}

// ---------------------------------------------------------------------------
// /settings
// ---------------------------------------------------------------------------

const settingsCommand: CommandHandler = {
  data: new SlashCommandBuilder()
    .setName('settings')
    .setDescription('Bot settings')
    .setDMPermission(false)
    .addSubcommand((sub) =>
      sub
        .setName('alert')
        .setDescription(
          'Where to notify when a feed of THIS channel fails (run the command in that channel)',
        )
        .addUserOption((o) =>
          o.setName('user').setDescription('User to DM on failures (empty to clear)'),
        )
        .addChannelOption((o) =>
          o.setName('channel').setDescription('Channel to post failures in (empty to clear)'),
        ),
    ) as SlashCommandBuilder,

  async handle(context, interaction) {
    if (interaction.options.getSubcommand(true) !== 'alert') {
      await reply(interaction, 'Unknown subcommand.')
      return
    }
    // Settings are PER CHANNEL: run the command in the channel to configure.
    const channelId = interaction.channelId
    if (!interaction.inCachedGuild() || !(await canManageChannel(interaction, channelId))) {
      await reply(
        interaction,
        '🔒 You need the **Send Messages** permission in this channel to configure its alerts.',
      )
      return
    }
    const alertChannelOption = interaction.options.getChannel('channel')
    if (alertChannelOption && !(await canManageChannel(interaction, alertChannelOption.id))) {
      await denyChannelAccess(interaction, alertChannelOption.id)
      return
    }

    const user = interaction.options.getUser('user')
    const channel = interaction.options.getChannel('channel')
    if (user === null) {
      context.store.setChannelSetting(channelId, 'alert_user_id', null)
    } else if (user) {
      context.store.setChannelSetting(channelId, 'alert_user_id', user.id)
    }
    if (channel === null) {
      context.store.setChannelSetting(channelId, 'alert_channel_id', null)
    } else if (channel) {
      context.store.setChannelSetting(channelId, 'alert_channel_id', channel.id)
    }

    const current: string[] = []
    const alertUser = context.store.getChannelSetting(channelId, 'alert_user_id')
    const alertChannel = context.store.getChannelSetting(channelId, 'alert_channel_id')
    if (alertUser) current.push(`User: <@${alertUser}>`)
    if (alertChannel) current.push(`Channel: <#${alertChannel}>`)
    await reply(
      interaction,
      `⚙️ Failure alerts for <#${channelId}>: ${current.length > 0 ? current.join(' · ') : 'disabled'}`,
    )
  },
}

// ---------------------------------------------------------------------------
// /status
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// /debug
// ---------------------------------------------------------------------------

const debugCommand: CommandHandler = {
  data: new SlashCommandBuilder()
    .setName('debug')
    .setDescription('Diagnostics for troubleshooting')
    .setDMPermission(false)
    .addSubcommand((sub) =>
      sub
        .setName('feed')
        .setDescription('Dry-run a feed: fetch now and report what would happen (no state change)')
        .addStringOption((o) => feedIdOption(o)),
    )
    .addSubcommand((sub) =>
      sub.setName('system').setDescription('System diagnostics (memory, queues, scheduler)'),
    ) as SlashCommandBuilder,

  async handle(context, interaction) {
    const sub = interaction.options.getSubcommand(true)
    if (sub === 'feed') {
      await interaction.deferReply({ flags: MessageFlags.Ephemeral })
      const feed = await getFeedOrReply(context, interaction)
      if (!feed) return

      let report: DryRunReport
      try {
        report = await context.pipeline.dryRun(feed.id)
      } catch (error) {
        await interaction.editReply(`❌ ${error instanceof Error ? error.message : String(error)}`)
        return
      }

      const lines: string[] = [
        `🔧 **Dry-run — ${report.feed.name}** (\`${report.feed.id.slice(0, 8)}\`)`,
      ]
      lines.push(`URL: <${report.feed.url}> → <#${report.feed.channelId}>`)
      lines.push(
        `Enabled: ${report.feed.enabled}${report.feed.pauseReason ? ` (${report.feed.pauseReason})` : ''} · interval ${report.feed.intervalMinutes} min · batch \`${report.feed.batchMode}\` (≥ ${report.feed.batchThreshold})`,
      )
      lines.push(
        `State: ${report.state.seenCount} seen · failures ×${report.state.consecutiveFailures}${report.state.lastError ? ` — last: ${report.state.lastError}` : ''} · queue: ${report.state.queueDepth}`,
      )

      const fetchInfo = report.fetch
      if (!fetchInfo) {
        lines.push('Fetch: (no result)')
      } else if (fetchInfo.status === 'error') {
        lines.push(`Fetch: ❌ ${fetchInfo.error}`)
        lines.push('→ Check the URL, the container logs and your network from the host.')
      } else if (fetchInfo.status === 'not_modified') {
        lines.push(`Fetch: ✅ ${fetchInfo.detail}`)
      } else {
        lines.push(
          `Fetch: ✅ ${fetchInfo.articleCount} article(s) parsed${fetchInfo.feedTitle ? ` (feed title: "${fetchInfo.feedTitle}")` : ''}`,
        )
      }

      if (report.detection) {
        const d = report.detection
        lines.push(
          `Detection: ${d.fresh} new · ${d.blocked} blocked by filters · ${d.tooOld} too old`,
        )
        if (d.freshTitles.length > 0) {
          lines.push('New (would be sent):')
          for (const title of d.freshTitles) lines.push(`  - ${title}`)
        }
        if (d.blockedTitles.length > 0) {
          lines.push('Blocked:')
          for (const title of d.blockedTitles) lines.push(`  - ${title}`)
        }
      }
      if (report.delivery) {
        lines.push(
          `Delivery: \`${report.delivery.mode}\` → ${report.delivery.messages} message(s)${report.delivery.wouldSendNow ? '' : ' — **nothing would be sent right now** (paused, nothing new, or first check not yet done)'}`,
        )
      }
      if (report.render) {
        lines.push(
          `Render preview (latest article "${report.render.articleTitle}", ${report.render.embedCount} embed):`,
        )
        lines.push('```')
        lines.push(report.render.content.slice(0, 500) || '(empty content)')
        lines.push('```')
      }
      lines.push('*No state was modified by this dry-run.*')

      const chunks = chunkLines(lines)
      await interaction.editReply(chunks[0] ?? 'No data.')
      for (const chunk of chunks.slice(1, 5)) {
        await interaction.followUp({ content: chunk, flags: MessageFlags.Ephemeral })
      }
      return
    }

    // /debug system
    const memory = process.memoryUsage()
    const uptimeS = Math.floor(process.uptime())
    const feeds = context.store.getAllFeeds()
    const lines = [
      '🔧 **System diagnostics**',
      `Version: ${context.version} · Node ${process.version}`,
      `Uptime: ${Math.floor(uptimeS / 3600)}h ${Math.floor((uptimeS % 3600) / 60)}m`,
      `Memory: RSS ${(memory.rss / 1024 / 1024).toFixed(0)} MB · heap ${(memory.heapUsed / 1024 / 1024).toFixed(0)}/${(memory.heapTotal / 1024 / 1024).toFixed(0)} MB`,
      `Feeds: ${feeds.filter((f) => f.enabled).length}/${feeds.length} enabled`,
      `Send queue: ${context.sender.totalDepth()} message(s) pending`,
      `Scheduler: ${context.scheduler.isSuspended() ? 'suspended (import/restore)' : 'running'}`,
      `Bot user: ${context.client.user?.tag ?? 'not ready'}`,
      '',
      '💡 Container logs: `docker logs -f rrs-tracker` (or the add-on log page).',
    ]
    await reply(interaction, chunkLines(lines)[0] ?? 'No data.')
  },
}

const statusCommand: CommandHandler = {
  data: new SlashCommandBuilder()
    .setName('status')
    .setDescription('Bot health status')
    .setDMPermission(false) as SlashCommandBuilder,

  async handle(context, interaction) {
    const feeds = context.store.getAllFeeds()
    const enabled = feeds.filter((f) => f.enabled).length
    const failing = feeds.filter((f) => f.consecutiveFailures > 0)
    const lines = [
      `🤖 **RRS-Tracker**`,
      `Feeds: ${enabled}/${feeds.length} enabled, ${feeds.length - enabled} paused`,
      `Failing: ${failing.length}`,
      `Send queue: ${context.sender.totalDepth()} message(s) pending`,
      `Scheduler: ${context.scheduler.isSuspended() ? 'suspended (import/restore)' : 'running'}`,
    ]
    if (failing.length > 0) {
      lines.push(
        '',
        '**Failing feeds:**',
        ...failing
          .slice(0, 10)
          .map((f) => `- **${f.name}**: ${f.lastError ?? 'unknown'} (×${f.consecutiveFailures})`),
      )
    }
    const chunks = chunkLines(lines)
    await reply(interaction, chunks[0] ?? 'Nothing to display.')
  },
}

// ---------------------------------------------------------------------------
// Modal submits
// ---------------------------------------------------------------------------

async function handleModalSubmit(
  context: BotContext,
  interaction: ModalSubmitInteraction,
): Promise<void> {
  const [customId, feedId] = interaction.customId.split(':')
  if (!feedId) return
  if (!interaction.inGuild()) return

  const feed = context.store.getFeed(feedId)
  if (!feed) {
    await interaction.reply({ content: 'Feed no longer exists.', flags: MessageFlags.Ephemeral })
    return
  }
  // Modals edit a feed's template: enforce the channel-scoped permission.
  if (
    feed.guildId !== interaction.guildId ||
    !(await canManageChannel(interaction, feed.channelId))
  ) {
    await interaction.reply({
      content: `🔒 You need the **Send Messages** permission in <#${feed.channelId}> to edit this feed's templates.`,
      flags: MessageFlags.Ephemeral,
    })
    return
  }

  if (customId === 'feed-template') {
    const template = interaction.fields.getTextInputValue('template')
    context.store.updateFeed(feedId, { templateText: template })
    await interaction.reply({
      content: `✅ Template updated for **${feed.name}**.`,
      flags: MessageFlags.Ephemeral,
    })
    return
  }
  if (customId === 'feed-embed') {
    const raw = interaction.fields.getTextInputValue('embed').trim()
    if (raw.length > 0) {
      try {
        JSON.parse(raw)
      } catch (error) {
        await interaction.reply({
          content: `❌ Invalid JSON: ${error instanceof Error ? error.message : String(error)}`,
          flags: MessageFlags.Ephemeral,
        })
        return
      }
    }
    context.store.updateFeed(feedId, { embedJson: raw.length > 0 ? raw : null })
    await interaction.reply({
      content:
        raw.length > 0
          ? `✅ Embed updated for **${feed.name}**.`
          : `✅ Embed removed for **${feed.name}**.`,
      flags: MessageFlags.Ephemeral,
    })
  }
}

// ---------------------------------------------------------------------------
// Registry (declared after all handlers)
// ---------------------------------------------------------------------------

export const commands: CommandHandler[] = [
  feedCommand,
  importCommand,
  backupCommand,
  settingsCommand,
  statusCommand,
  debugCommand,
]
