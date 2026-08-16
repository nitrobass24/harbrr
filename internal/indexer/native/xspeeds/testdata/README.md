# XSpeeds fixtures

Behavioral oracle: Prowlarr `XSpeeds.cs` at
`1f7db1e651249f1a3da0d8b55fbc0b2dd980b37a`. Jackett `XSpeeds.cs` at
`80f78dec295d3c4a0dc8170866e7ad558ec56630` is divergence evidence only.

Contract: base URL `https://www.xspeeds.eu/`; `GET login.php`; `POST takelogin.php`;
`GET browse.php`; authenticated `download.php` grabs. harbrr uses a conservative
2.1-second request floor, interprets zone-less tracker dates as UTC, and excludes
Jackett-only CAPTCHA, IMDB, POST-search, sorting, and category 166/167 behavior.

All fixtures are hand-authored synthetic HTML. They contain no live pages or real
credentials. Rows without a usable title/details or download link are skipped. A row
without a category remains uncategorized. Malformed size/stat fields become zero;
missing or malformed dates remain empty.

`ponytail: CAPTCHA unsupported; add it only when the live site or selected oracle requires it.`
