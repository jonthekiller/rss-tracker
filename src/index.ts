import { startBot } from './bot/bot.js'

startBot().catch((error) => {
  // eslint-disable-next-line no-console
  console.error('Fatal startup error:', error)
  process.exit(1)
})
