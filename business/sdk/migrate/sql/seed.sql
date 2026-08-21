INSERT INTO integration_providers (
		id,
		name,
		icon,
		type,
		description,
		fields,
		sort_order
	)
VALUES (
		'slack',
		'Slack',
		'💬',
		'Messaging',
		'Send alerts to Slack channels via webhook.',
		'[{"k":"webhookUrl","label":"Webhook URL","ph":"https://hooks.slack.com/services/..."}]',
		1
	),
	(
		'discord',
		'Discord',
		'🎮',
		'Messaging',
		'Forward log alerts to Discord via webhook.',
		'[{"k":"webhookUrl","label":"Webhook URL","ph":"https://discord.com/api/webhooks/..."}]',
		2
	),
	(
		'telegram',
		'Telegram',
		'✈️',
		'Messaging',
		'Receive alerts as Telegram bot messages.',
		'[{"k":"botToken","label":"Bot Token","ph":"123456:ABC..."},{"k":"chatId","label":"Chat ID","ph":"-100123"}]',
		3
	),
	(
		'pagerduty',
		'PagerDuty',
		'🚨',
		'Incident',
		'Auto-create PagerDuty incidents on critical errors.',
		'[{"k":"apiKey","label":"API Key","ph":"u+xxxxxxxx"},{"k":"serviceId","label":"Service ID","ph":"P1234AB"}]',
		4
	),
	(
		'webhook',
		'Webhook',
		'🔗',
		'Custom',
		'POST structured JSON to any HTTP endpoint.',
		'[{"k":"url","label":"Target URL","ph":"https://yourapp.com/hook"},{"k":"secret","label":"Secret","ph":"optional HMAC secret"}]',
		5
	),
	(
		'email',
		'Email',
		'📧',
		'Notify',
		'Send email alerts when log events trigger.',
		'[{"k":"to","label":"To Address","ph":"team@co.com"}]',
		6
	),
	(
		'opsgenie',
		'OpsGenie',
		'🔔',
		'Incident',
		'Create OpsGenie alerts for on-call escalation.',
		'[{"k":"apiKey","label":"API Key","ph":"xxxx-xxxx-xxxx"}]',
		7
	),
	(
		'jira',
		'Jira',
		'🧩',
		'Ticketing',
		'Open Jira issues automatically on ERROR logs.',
		'[{"k":"domain","label":"Domain","ph":"org.atlassian.net"},{"k":"email","label":"Account Email","ph":"you@org.com"},{"k":"token","label":"API Token","ph":"ATATT..."},{"k":"project","label":"Project Key","ph":"ENG"}]',
		8
	),
	(
		'twilio',
		'Twilio',
		'📱',
		'SMS',
		'Send SMS alerts to a phone number via Twilio.',
		'[{"k":"accountSid","label":"Account SID","ph":"ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},{"k":"authToken","label":"Auth Token","ph":""},{"k":"from","label":"From Number","ph":"+12345678900"},{"k":"to","label":"To Number","ph":"+12345678900"}]',
		9
	),
	(
		'beemsms',
		'Beem Africa',
		'📲',
		'SMS',
		'Send SMS alerts via Beem Africa.',
		'[{"k":"apiKey","label":"API Key","ph":""},{"k":"secretKey","label":"Secret Key","ph":""},{"k":"senderId","label":"Sender ID","ph":"MYAPP"},{"k":"to","label":"To Number","ph":"+255700000000"}]',
		10
	),
	(
		'msteams',
		'Microsoft Teams',
		'🟣',
		'Messaging',
		'Post alerts to a Teams channel via a Workflows webhook.',
		'[{"k":"webhookUrl","label":"Workflow URL","ph":"https://prod-00.westeurope.logic.azure.com:443/workflows/..."}]',
		11
	),
	(
		'googlechat',
		'Google Chat',
		'💠',
		'Messaging',
		'Post alerts to a Google Chat space via webhook.',
		'[{"k":"webhookUrl","label":"Webhook URL","ph":"https://chat.googleapis.com/v1/spaces/.../messages?key=..."}]',
		12
	),
	(
		'mattermost',
		'Mattermost',
		'🔷',
		'Messaging',
		'Post alerts to a Mattermost channel via an incoming webhook.',
		'[{"k":"webhookUrl","label":"Webhook URL","ph":"https://mattermost.example.com/hooks/xxxxxxxx"},{"k":"channel","label":"Channel override (optional)","ph":"alerts"}]',
		13
	),
	(
		'whatsapp',
		'WhatsApp',
		'🟢',
		'Messaging',
		'Send alerts to WhatsApp via the Business Cloud API. Set a template name — free-form text is only delivered within 24 hours of the recipient messaging you.',
		'[{"k":"phoneNumberId","label":"Phone Number ID","ph":"123456789012345"},{"k":"accessToken","label":"Access Token","ph":""},{"k":"to","label":"To Number","ph":"+255700000000"},{"k":"templateName","label":"Template Name (recommended)","ph":"streamlogia_alert"},{"k":"templateLanguage","label":"Template Language (optional)","ph":"en"}]',
		14
	),
	(
		'github',
		'GitHub',
		'🐙',
		'Ticketing',
		'Open a GitHub issue when an alert fires.',
		'[{"k":"owner","label":"Owner","ph":"my-org"},{"k":"repo","label":"Repository","ph":"my-service"},{"k":"token","label":"Access Token","ph":"github_pat_..."},{"k":"labels","label":"Labels (optional, comma separated)","ph":"incident,logs"},{"k":"apiBaseUrl","label":"API Base URL (optional, Enterprise)","ph":"https://github.example.com/api/v3"}]',
		15
	),
	(
		'linear',
		'Linear',
		'📐',
		'Ticketing',
		'Create a Linear issue when an alert fires.',
		'[{"k":"apiKey","label":"API Key","ph":"lin_api_..."},{"k":"teamId","label":"Team ID","ph":"a1b2c3d4-..."},{"k":"priority","label":"Priority 0-4 (optional)","ph":"2"}]',
		16
	),
	(
		'africastalking',
		'Africa''s Talking',
		'📶',
		'SMS',
		'Send SMS alerts via Africa''s Talking.',
		'[{"k":"username","label":"Username","ph":"myapp"},{"k":"apiKey","label":"API Key","ph":""},{"k":"to","label":"To Number","ph":"+254700000000"},{"k":"from","label":"Sender ID (optional)","ph":"MYAPP"},{"k":"sandbox","label":"Sandbox? true/false (optional)","ph":"false"}]',
		17
	) ON CONFLICT (id) DO NOTHING;
INSERT INTO users (
		id,
		name,
		email,
		roles,
		password_hash,
		enabled,
		date_created,
		date_updated
	)
VALUES (
		'231c6f21-0207-4d5c-bc83-a4fdbd5cb06f',
		'Alfie Solomon',
		'alfie@logingestor.com',
		'{SUPER ADMIN}',
		'$2a$10$1ggfMVZV6Js0ybvJufLRUOWHS5f6KneuP0XwwHpJ8L8ipdry9f2/a',
		true,
		'2019-03-24 00:00:00',
		'2019-03-24 00:00:00'
	),
	(
		'45b5fbd3-755f-4379-8f07-a58d4a30fa2f',
		'User Gopher',
		'user@example.com',
		'{VIEWER}',
		'$2a$10$9/XASPKBbJKVfCAZKDH.UuhsuALDr5vVm6VrYA9VFR8rccK86C1hW',
		true,
		'2019-03-24 00:00:00',
		'2019-03-24 00:00:00'
	) ON CONFLICT DO NOTHING;