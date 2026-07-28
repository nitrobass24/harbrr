# VIP & membership expiry

Many private trackers time-box what you have: VIP bought with a donation, premium earned with
bonus points, or the account itself on trackers that prune inactive members. harbrr can hold the
expiry date for each indexer and make sure it never passes unannounced.

## Why you want this

The failure this feature exists for is **silent**. When VIP lapses, nothing breaks — every
search and grab still succeeds. What changes is what they *cost you*:

- Global freeleech quietly stops, and your automation keeps grabbing at the same rate — except
  now every download counts against your ratio. You find out when your buffer has already
  dropped, days or weeks later.
- A ratio-requirement exemption ends, dropping you under rules your download habits were never
  shaped to satisfy.
- On some trackers, the account itself lapses — and on an invite-only tracker that can be
  unrecoverable no matter what you'd pay.

Nothing in harbrr's health monitoring can see any of that, because every request keeps
succeeding. A date is the only signal, so harbrr tracks the date.

## Setting it up

Edit an indexer and open its **Advanced** options. The **Expiry** group has three fields:

- **Expiry** — the date, e.g. `2026-08-01`. It is stored as a plain calendar date, so no
  timezone arithmetic can ever shift it by a day.
- **Expiry type** — *Perk* (VIP/premium lapses but your account survives), *Account* (your
  access itself ends), or leave it unset and harbrr just calls it "Membership".
- **Lifetime (never expires)** — for lifetime VIP. Ticking it disables the date and turns all
  warnings off for that indexer.

Leave everything empty and nothing changes: this is opt-in bookkeeping, not a nag. An indexer
with nothing set costs harbrr one empty database read per hour, fleet-wide.

## What you get

**Warnings ahead of time.** By default harbrr notifies at **30, 14, 7, and 1 day(s)** before the
date, and once more **on the day itself**. The lead times are tunable
(`GET/PUT /api/config/expiry-thresholds`, a comma-separated list of days), but the at-expiry
warning always fires regardless of configuration: an expiry passing unannounced is the exact failure this feature exists to
prevent, so it is not something a settings value can switch off.

Warnings go through the same notification targets you already use (Discord, webhook) — enable
the *indexer expiry* event on the notification. When harbrr knows its external URL, the message
links straight to the indexers page, where the date is one field away from renewed.

**Exactly one message per milestone.** Each threshold fires once — surviving restarts — and if
harbrr was offline across several milestones, you get **one** message describing where things
stand today, not a backlog. Once expired, the row keeps saying so in the UI, but harbrr does not
ping you daily about a fact you already know.

**Renewal just works.** When you renew, update the date. That alone re-arms the whole warning
ladder for the new date — there is nothing else to reset or clear.

**Visibility where you decide.** The indexers list gains a pressable **Expiry** column: expired
first, soonest next, untracked and lifetime indexers sunk to the bottom. Rows within seven days
of expiry change tone so an approaching date reads as a warning, not trivia.

Your dates survive backup and restore. (The warning history deliberately does not — a restored
instance warns once more, which errs in the safe direction.)

## Honest limitations

- **The date is yours to enter.** harbrr does not fetch it from the tracker — that would mean
  scraping account pages per tracker, and a silently wrong scraped date is worse than no date on
  exactly the field where being wrong costs an account. You learn the date once, when you pay;
  entering it takes ten seconds and works on every tracker harbrr supports, today.
- **If you renew at the tracker and forget to update harbrr**, you'll get a warning you didn't
  need. The message links you to the fix. That is still strictly better than the alternative,
  where you get no warning and find out from your ratio.
- The warnings tell you; they can't renew for you. There is no button inside a Discord message —
  the link takes you to where the date is edited.
