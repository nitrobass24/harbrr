# Tracker coverage & status

Every tracker harbrr knows about — the embedded Cardigann corpus plus the native drivers
Cardigann can't express — and how far each is validated.

- **Built** ✅ — harbrr serves it (a Cardigann definition ships, or a native Go driver does) and
  it passes its offline golden tests. ⬜ means a driver is planned but not built yet.
- **Live-tested** ✅ — confirmed against the real tracker: a Prowlarr differential plus a real
  grab. ⬜ means built and offline-validated, but not yet live-verified (usually just needs an
  account on that tracker). See **[Test status](test-status.md)** for the evidence behind this
  column and the auth/fetch patterns proven live.

**597 trackers** total: 554 Cardigann corpus (all built) · 24 native drivers built · 19 native
drivers planned. To configure one, see **[Adding an indexer](guides/add-indexer.md)**.

## Native drivers

Bespoke code in Jackett/Prowlarr (no Cardigann definition); harbrr ships native Go drivers.

| Tracker | Pattern | Built | Live-tested |
|---|---|:--:|:--:|
| AvistaZ | Bearer (login → token) | ✅ | ⬜ |
| CinemaZ | Bearer (login → token) | ✅ | ⬜ |
| PrivateHD | Bearer (login → token) | ✅ | ⬜ |
| ExoticaZ | Bearer (login → token) | ✅ | ⬜ |
| IPTorrents | Session cookie (HTML scrape) | ✅ | ✅ |
| TorrentDay | Session cookie (HTML scrape) | ✅ | ⬜ |
| FileList | Passkey / JSON API | ✅ | ✅ |
| HDBits | Passkey / JSON API | ✅ | ✅ |
| BeyondHD | Passkey / JSON API | ✅ | ⬜ |
| MyAnonamouse | Session cookie (rotating, JSON) | ✅ | ✅ |
| Redacted | Gazelle (cookie/key → ajax.php) | ✅ | ⬜ |
| Orpheus | Gazelle (cookie/key → ajax.php) | ✅ | ⬜ |
| AlphaRatio | Gazelle (session cookie → ajax.php) | ✅ | ⬜ |
| BroadcastTheNet | Bespoke API | ✅ | ✅ |
| PassThePopcorn | Bespoke API | ✅ | ✅ |
| GazelleGames | Bespoke API | ✅ | ⬜ |
| AnimeBytes | Bespoke API | ✅ | ⬜ |
| Nebulance | Bespoke JSON API | ✅ | ⬜ |
| Usenet (Newznab) | Generic Newznab | ✅ | ✅ |
| NZBIndex | Bespoke JSON API (public) | ✅ | ✅ |
| MoreThanTV | Torznab API (native) | ✅ | ✅ |
| AnimeTosho | Torznab API (native) | ✅ | ⬜ |
| Torrent Network | Torznab API (native) | ✅ | ⬜ |
| Torznab (generic) | Generic Torznab | ✅ | ⬜ |

### Planned — vote for yours

Native drivers we have issues for but haven't built. 👍 or comment on the issue for the one you want and it moves up the queue.

| Tracker | Pattern | Built | Live-tested |
|---|---|:--:|:--:|
| [SpeedCD](https://github.com/autobrr/harbrr/issues/21) | Session cookie (HTML scrape) | ⬜ | ⬜ |
| [FunFile](https://github.com/autobrr/harbrr/issues/23) | Session cookie (HTML scrape) | ⬜ | ⬜ |
| [BitHDTV](https://github.com/autobrr/harbrr/issues/24) | Session cookie (HTML scrape) | ⬜ | ⬜ |
| [TorrentBytes](https://github.com/autobrr/harbrr/issues/33) | Session cookie (HTML scrape) | ⬜ | ⬜ |
| [XSpeeds](https://github.com/autobrr/harbrr/issues/34) | Session cookie (HTML scrape) | ⬜ | ⬜ |
| [PreToMe](https://github.com/autobrr/harbrr/issues/35) | Session cookie (HTML scrape) | ⬜ | ⬜ |
| [RevolutionTT](https://github.com/autobrr/harbrr/issues/36) | Session cookie (HTML scrape) | ⬜ | ⬜ |
| [MTeam](https://github.com/autobrr/harbrr/issues/25) | Passkey / JSON API | ⬜ | ⬜ |
| [NorBits](https://github.com/autobrr/harbrr/issues/26) | Passkey / JSON API | ⬜ | ⬜ |
| [SceneHD](https://github.com/autobrr/harbrr/issues/27) | Passkey / JSON API | ⬜ | ⬜ |
| [DICMusic](https://github.com/autobrr/harbrr/issues/28) | Gazelle (username / password) | ⬜ | ⬜ |
| [Libble](https://github.com/autobrr/harbrr/issues/29) | Gazelle (username / password) | ⬜ | ⬜ |
| [GreatPosterWall](https://github.com/autobrr/harbrr/issues/30) | Gazelle (username / password) | ⬜ | ⬜ |
| [BrokenStones](https://github.com/autobrr/harbrr/issues/31) | Gazelle (username / password) | ⬜ | ⬜ |
| [RuTracker](https://github.com/autobrr/harbrr/issues/37) | Public / niche | ⬜ | ⬜ |
| [LostFilm](https://github.com/autobrr/harbrr/issues/38) | Public / niche | ⬜ | ⬜ |
| [Toloka](https://github.com/autobrr/harbrr/issues/39) | Public / niche | ⬜ | ⬜ |
| [SubsPlease](https://github.com/autobrr/harbrr/issues/40) | Public / niche | ⬜ | ⬜ |
| [AudioBookBay](https://github.com/autobrr/harbrr/issues/41) | Public / niche | ⬜ | ⬜ |

## Cardigann corpus

Served through the shared engine from the vendored Jackett snapshot — all built. Live-tested where an operator instance covers them.

### Private (407)

| Tracker | Built | Live-tested |
|---|:--:|:--:|
| 0day.kiev | ✅ | ⬜ |
| 13City | ✅ | ⬜ |
| 1ptbar | ✅ | ⬜ |
| 3D Torrents | ✅ | ⬜ |
| 4thD | ✅ | ⬜ |
| 52PT | ✅ | ⬜ |
| 720pier | ✅ | ⬜ |
| Abnormal (API) | ✅ | ⬜ |
| ABtorrents | ✅ | ⬜ |
| Across The Tasman | ✅ | ⬜ |
| Aftershock | ✅ | ⬜ |
| AGSVPT | ✅ | ⬜ |
| Aidoru!Online | ✅ | ⬜ |
| Aither (API) | ✅ | ✅ |
| alingPT | ✅ | ⬜ |
| Amigos Share Club | ✅ | ⬜ |
| AnimeTorrents.ro | ✅ | ⬜ |
| AnimeWorld (API) | ✅ | ⬜ |
| Anthelion (API) | ✅ | ✅ |
| ArabaFenice | ✅ | ⬜ |
| ArabicSource (API) | ✅ | ⬜ |
| ArabP2P | ✅ | ⬜ |
| ArabScene | ✅ | ⬜ |
| ArabTorrents | ✅ | ⬜ |
| AsianCinema | ✅ | ⬜ |
| AsianDVDClub | ✅ | ⬜ |
| Audiences | ✅ | ⬜ |
| AudioNews | ✅ | ⬜ |
| AURA4K (API) | ✅ | ✅ |
| Aussierul.es | ✅ | ⬜ |
| Azusa (梓喵) | ✅ | ⬜ |
| Back-ups | ✅ | ⬜ |
| baoziPT | ✅ | ⬜ |
| Beload | ✅ | ⬜ |
| Best-Core | ✅ | ⬜ |
| Bibliotik | ✅ | ⬜ |
| BigBBS | ✅ | ⬜ |
| BigCore | ✅ | ⬜ |
| Bit-Bázis | ✅ | ⬜ |
| Bitded | ✅ | ⬜ |
| bitGAMER | ✅ | ⬜ |
| BitHUmen | ✅ | ⬜ |
| Bitpalace | ✅ | ⬜ |
| BitPorn (API) | ✅ | ⬜ |
| Bittorrentfiles | ✅ | ⬜ |
| BiTTuRK | ✅ | ⬜ |
| BJ-Share | ✅ | ⬜ |
| BlueBird | ✅ | ⬜ |
| BlueTorrents | ✅ | ⬜ |
| BlurayTracker | ✅ | ⬜ |
| Blutopia (API) | ✅ | ⬜ |
| Borgzelle | ✅ | ⬜ |
| Boxing Torrents | ✅ | ⬜ |
| BTArg | ✅ | ⬜ |
| BTNext | ✅ | ⬜ |
| BTSCHOOL | ✅ | ⬜ |
| BwTorrents | ✅ | ⬜ |
| BYRBT | ✅ | ⬜ |
| C411 | ✅ | ⬜ |
| Caishen (财神) | ✅ | ⬜ |
| cangbaoge (CBG) | ✅ | ⬜ |
| CapybaraBR (API) | ✅ | ⬜ |
| Carpathians | ✅ | ⬜ |
| CarPT | ✅ | ⬜ |
| Cathode-Ray.Tube | ✅ | ⬜ |
| CeskeForum | ✅ | ⬜ |
| CGPeers | ✅ | ⬜ |
| CHDBits | ✅ | ⬜ |
| ChileBT (API) | ✅ | ⬜ |
| Cinemageddon | ✅ | ⬜ |
| CinemaMovieS_ZT | ✅ | ⬜ |
| Cinematik (API) | ✅ | ⬜ |
| ClearJAV (API) | ✅ | ⬜ |
| Coastal-Music-Crew | ✅ | ⬜ |
| ConCen | ✅ | ⬜ |
| Concertos (API) | ✅ | ⬜ |
| CrabPT (蟹黄堡) | ✅ | ⬜ |
| CrazySpirits (API) | ✅ | ⬜ |
| CrnaBerza | ✅ | ⬜ |
| CrnaBerza2FA | ✅ | ⬜ |
| CRT2FA | ✅ | ⬜ |
| cyanbug (大青虫) | ✅ | ⬜ |
| CyclingArchiveClub | ✅ | ⬜ |
| CZTeam (API) | ✅ | ⬜ |
| Darkpeers (API) | ✅ | ✅ |
| Das Unerwartete | ✅ | ⬜ |
| Das Unerwartete (API) | ✅ | ⬜ |
| DataScene (API) | ✅ | ⬜ |
| Depth Studio | ✅ | ⬜ |
| DesiTorrents (API) | ✅ | ⬜ |
| Diablo Torrent | ✅ | ⬜ |
| DigitalCore (API) | ✅ | ✅ |
| DimeADozen | ✅ | ⬜ |
| DiscFan | ✅ | ⬜ |
| DocsPedia | ✅ | ⬜ |
| Drugari | ✅ | ⬜ |
| DS-Reloaded | ✅ | ⬜ |
| dubhe (天枢) | ✅ | ⬜ |
| Ebooks-Shares | ✅ | ⬜ |
| Empornium | ✅ | ⬜ |
| Empornium2FA | ✅ | ⬜ |
| eMuwarez (API) | ✅ | ⬜ |
| eStone | ✅ | ⬜ |
| exitorrent.org | ✅ | ⬜ |
| Explosiv-World | ✅ | ⬜ |
| ExtremeBits | ✅ | ⬜ |
| F1Carreras (API) | ✅ | ⬜ |
| F1GP | ✅ | ⬜ |
| FANO.IN | ✅ | ⬜ |
| Fappaizuri | ✅ | ⬜ |
| Fappaizuri2FA | ✅ | ⬜ |
| Femdomcult | ✅ | ⬜ |
| FinElite | ✅ | ⬜ |
| Flood | ✅ | ⬜ |
| Free Farm (自由农场) | ✅ | ⬜ |
| FunkyTorrents | ✅ | ⬜ |
| funZone (API) | ✅ | ⬜ |
| FutureTorrent | ✅ | ⬜ |
| Fuzer | ✅ | ⬜ |
| G3MINI TR4CK3R (API) | ✅ | ⬜ |
| GAYtorrent.ru | ✅ | ⬜ |
| Generation-Free (API) | ✅ | ⬜ |
| GGPT | ✅ | ⬜ |
| GigaTorrents | ✅ | ⬜ |
| Girotorrent | ✅ | ⬜ |
| HaiDan | ✅ | ⬜ |
| HappyFappy | ✅ | ⬜ |
| HappyFappy2FA | ✅ | ⬜ |
| hawke-uno | ✅ | ⬜ |
| HD Dolby | ✅ | ⬜ |
| HD-CLUB | ✅ | ⬜ |
| HD-Forever | ✅ | ⬜ |
| HD-Forever2FA | ✅ | ⬜ |
| HD-Olimpo (API) | ✅ | ⬜ |
| HD-Only (API) | ✅ | ⬜ |
| HD-Space | ✅ | ✅ |
| HD-Torrents | ✅ | ⬜ |
| HD-UNiT3D (API) | ✅ | ⬜ |
| HDArea | ✅ | ⬜ |
| HDBao | ✅ | ⬜ |
| HDCiTY | ✅ | ⬜ |
| HDClone | ✅ | ⬜ |
| HDFans | ✅ | ⬜ |
| HDHome | ✅ | ⬜ |
| HDKylin (麒麟) | ✅ | ⬜ |
| HDRoute | ✅ | ⬜ |
| HDSky | ✅ | ⬜ |
| HDtime | ✅ | ⬜ |
| HDTorrents.it (API) | ✅ | ⬜ |
| HDTurk | ✅ | ⬜ |
| HDU | ✅ | ⬜ |
| HDVideo | ✅ | ⬜ |
| HDZero (API) | ✅ | ⬜ |
| Hebits | ✅ | ⬜ |
| HellasHut | ✅ | ⬜ |
| HellasHut (API) | ✅ | ⬜ |
| HELLENIC-HD (API) | ✅ | ⬜ |
| HeroBit | ✅ | ⬜ |
| HHanClub | ✅ | ⬜ |
| HHD (API) | ✅ | ⬜ |
| House of Devil | ✅ | ⬜ |
| HQMusic | ✅ | ⬜ |
| HUDBT (蝴蝶) | ✅ | ⬜ |
| HxPT (好学) | ✅ | ⬜ |
| Hǎitáng (海棠PT) | ✅ | ⬜ |
| Immortuos | ✅ | ⬜ |
| Indietorrents | ✅ | ⬜ |
| InfinityHD (API) | ✅ | ⬜ |
| Infire | ✅ | ⬜ |
| Insane Tracker | ✅ | ⬜ |
| ItaTorrents | ✅ | ⬜ |
| JME-REUNIT3D (API) | ✅ | ⬜ |
| JPopsuki | ✅ | ⬜ |
| JPTV4us (API) | ✅ | ⬜ |
| KamePT | ✅ | ⬜ |
| Karagarga | ✅ | ⬜ |
| Keep Friends | ✅ | ⬜ |
| Kelu | ✅ | ⬜ |
| Korsar | ✅ | ⬜ |
| KrazyZone | ✅ | ⬜ |
| Kufei (库非) | ✅ | ⬜ |
| Kufirc | ✅ | ⬜ |
| Kufirc2FA | ✅ | ⬜ |
| Kunlun (昆仑) | ✅ | ⬜ |
| lajidui | ✅ | ⬜ |
| Last Digital Underground | ✅ | ⬜ |
| LastFiles | ✅ | ⬜ |
| Lat-Team (API) | ✅ | ⬜ |
| LearnFlakes | ✅ | ⬜ |
| leech24 | ✅ | ⬜ |
| LemonHD.net | ✅ | ⬜ |
| LeSaloon | ✅ | ⬜ |
| LetSeed | ✅ | ⬜ |
| LibraNet | ✅ | ⬜ |
| LinkoManija | ✅ | ⬜ |
| Locadora (API) | ✅ | ⬜ |
| LongPT | ✅ | ⬜ |
| LosslessClub | ✅ | ⬜ |
| LP-Bits 2.0 | ✅ | ⬜ |
| LST | ✅ | ✅ |
| LuckPT | ✅ | ⬜ |
| Luminarr (API) | ✅ | ✅ |
| MaDs Revolution | ✅ | ⬜ |
| Magico | ✅ | ⬜ |
| Majomparádé | ✅ | ⬜ |
| Making Off | ✅ | ⬜ |
| Malayabits (API) | ✅ | ⬜ |
| March | ✅ | ⬜ |
| Matrix | ✅ | ⬜ |
| MDAN | ✅ | ⬜ |
| Memphis | ✅ | ⬜ |
| MeseVilág | ✅ | ⬜ |
| MetalGuru | ✅ | ⬜ |
| MidnightScene | ✅ | ⬜ |
| Milkie | ✅ | ⬜ |
| Milnueve (API) | ✅ | ⬜ |
| MMA-torrents | ✅ | ⬜ |
| MNV | ✅ | ⬜ |
| MOJBLiNK | ✅ | ⬜ |
| MomentPT | ✅ | ⬜ |
| MonikaDesign (API) | ✅ | ⬜ |
| MouseBits | ✅ | ⬜ |
| Musopia (音乐乌托邦) | ✅ | ⬜ |
| Muxuege | ✅ | ⬜ |
| MySpleen | ✅ | ⬜ |
| NanyangPT (南洋) | ✅ | ⬜ |
| nCore | ✅ | ⬜ |
| New Heaven | ✅ | ⬜ |
| NicePT | ✅ | ⬜ |
| Nirvana (API) | ✅ | ⬜ |
| njtupt (浦园) | ✅ | ⬜ |
| NOBS | ✅ | ⬜ |
| NordicBytes | ✅ | ⬜ |
| NordicQuality (API) | ✅ | ⬜ |
| Nostradamus | ✅ | ⬜ |
| NovaHD | ✅ | ⬜ |
| OKPT | ✅ | ⬜ |
| Old Greek Tracker (OGT) | ✅ | ⬜ |
| OldToonsWorld (API) | ✅ | ⬜ |
| OnlyEncodes+ (API) | ✅ | ✅ |
| OpenCD | ✅ | ⬜ |
| Order66 | ✅ | ⬜ |
| OshenPT | ✅ | ⬜ |
| OurBits | ✅ | ⬜ |
| P2PBG | ✅ | ⬜ |
| Panda | ✅ | ⬜ |
| ParabellumHD (API) | ✅ | ⬜ |
| Party-Tracker | ✅ | ⬜ |
| Peeratiko | ✅ | ⬜ |
| PeerGarden | ✅ | ⬜ |
| Peers.FM | ✅ | ⬜ |
| Phoenix Project | ✅ | ⬜ |
| PigNetwork (猪猪网) | ✅ | ⬜ |
| PixelCove | ✅ | ⬜ |
| PixelCove2FA | ✅ | ⬜ |
| PlayletPT | ✅ | ⬜ |
| Podzemlje | ✅ | ⬜ |
| Polish Torrent (API) | ✅ | ⬜ |
| PolishTracker (API) | ✅ | ⬜ |
| Pornbay | ✅ | ⬜ |
| Portugas (API) | ✅ | ⬜ |
| ProAudioTorrents | ✅ | ⬜ |
| PT GTK | ✅ | ⬜ |
| PTCafe (咖啡) | ✅ | ⬜ |
| PTCC (我的PT) | ✅ | ⬜ |
| PTCDY (传道院) | ✅ | ⬜ |
| PTerClub (PT之友俱乐部) | ✅ | ⬜ |
| PTFans | ✅ | ⬜ |
| PTFiles | ✅ | ⬜ |
| PThome | ✅ | ⬜ |
| PTLAO | ✅ | ⬜ |
| PTLGS | ✅ | ⬜ |
| PTSBAO (烧包) | ✅ | ⬜ |
| PTSKIT | ✅ | ⬜ |
| PTTey | ✅ | ⬜ |
| PTTime | ✅ | ⬜ |
| PTYING (樱花) | ✅ | ⬜ |
| PTzone | ✅ | ⬜ |
| PT分享站 (itzmx) | ✅ | ⬜ |
| Punk's Horror Tracker | ✅ | ⬜ |
| PuntoTorrent | ✅ | ⬜ |
| PuTao (葡萄) | ✅ | ⬜ |
| PWTorrents | ✅ | ⬜ |
| Qingwa (青蛙) | ✅ | ⬜ |
| R3V WTF! | ✅ | ⬜ |
| Racing4Everyone (API) | ✅ | ✅ |
| RacingForMe | ✅ | ✅ |
| RailgunPT | ✅ | ⬜ |
| Rain (雨) | ✅ | ⬜ |
| Rastastugan (API) | ✅ | ⬜ |
| Red Star Torrent | ✅ | ⬜ |
| ReelFLiX (API) | ✅ | ✅ |
| RetroMoviesClub (API) | ✅ | ✅ |
| RetroToon | ✅ | ⬜ |
| RocketHD (API) | ✅ | ⬜ |
| Romanian Metal Torrents | ✅ | ⬜ |
| Rousi.pro | ✅ | ⬜ |
| RunTheFrames (API) | ✅ | ⬜ |
| SAMARITANO (API) | ✅ | ⬜ |
| SBPT | ✅ | ⬜ |
| SceneRush | ✅ | ⬜ |
| SceneTime (API) | ✅ | ⬜ |
| SeedCore (API) | ✅ | ⬜ |
| SeedFile | ✅ | ⬜ |
| seedpool (API) | ✅ | ✅ |
| SewerPT | ✅ | ⬜ |
| SexTorrent (API) | ✅ | ⬜ |
| Shadowbit | ✅ | ⬜ |
| ShaKaw | ✅ | ⬜ |
| Shareisland (API) | ✅ | ⬜ |
| SiamBIT | ✅ | ⬜ |
| Siqi | ✅ | ⬜ |
| SkipTheCommercials (API) | ✅ | ⬜ |
| Slobit Games | ✅ | ⬜ |
| SnowPT | ✅ | ⬜ |
| SoulVoice (聆音Club) | ✅ | ⬜ |
| Speedmaster HD | ✅ | ⬜ |
| Spirit of Revolution | ✅ | ⬜ |
| SportsCora (API) | ✅ | ⬜ |
| SportsCult | ✅ | ⬜ |
| SpringSunday | ✅ | ⬜ |
| SunnyPT | ✅ | ⬜ |
| Superbits | ✅ | ⬜ |
| Swarmazon (API) | ✅ | ⬜ |
| TangPT (躺平) | ✅ | ⬜ |
| Tapochek | ✅ | ⬜ |
| Tasmanit | ✅ | ⬜ |
| Team CT Game | ✅ | ⬜ |
| TeamFlix | ✅ | ⬜ |
| TeamHD | ✅ | ⬜ |
| TeamOS | ✅ | ⬜ |
| TEKNO3D | ✅ | ⬜ |
| The Brothers | ✅ | ⬜ |
| The Crazy Ones | ✅ | ⬜ |
| The Empire | ✅ | ⬜ |
| The Falling Angels | ✅ | ⬜ |
| The Geeks | ✅ | ⬜ |
| The Kitchen | ✅ | ⬜ |
| The New Retro | ✅ | ⬜ |
| The Occult | ✅ | ⬜ |
| The Old School (API) | ✅ | ⬜ |
| The Paradiese | ✅ | ⬜ |
| The Place | ✅ | ⬜ |
| The Show | ✅ | ⬜ |
| The Vault | ✅ | ⬜ |
| The-New-Fun | ✅ | ⬜ |
| TheLeachZone (API) | ✅ | ⬜ |
| TJUPT (北洋园PT) | ✅ | ⬜ |
| TLFBits | ✅ | ⬜ |
| TmGHuB | ✅ | ⬜ |
| Toca Share (API) | ✅ | ⬜ |
| Tormac | ✅ | ⬜ |
| Tornado | ✅ | ⬜ |
| Torr9 | ✅ | ⬜ |
| Torrent Heaven | ✅ | ⬜ |
| Torrent Trader | ✅ | ⬜ |
| TOrrent-tuRK | ✅ | ⬜ |
| Torrent.LT | ✅ | ⬜ |
| TorrentAvenue (API) | ✅ | ⬜ |
| TorrentBD | ✅ | ⬜ |
| TorrentCCF | ✅ | ⬜ |
| TorrentClaw | ✅ | ⬜ |
| TorrentDD | ✅ | ⬜ |
| Torrenteros (API) | ✅ | ⬜ |
| TorrentHR (API) | ✅ | ⬜ |
| Torrenting | ✅ | ⬜ |
| TorrentLeech | ✅ | ✅ |
| Torrentleech.pl | ✅ | ⬜ |
| ToTheGlory | ✅ | ⬜ |
| ToTheGlory2FA | ✅ | ⬜ |
| TrackerMK | ✅ | ⬜ |
| TrackerZero | ✅ | ⬜ |
| TranceTraffic | ✅ | ⬜ |
| TreZzoR | ✅ | ⬜ |
| TreZzoRCookie | ✅ | ⬜ |
| TurkSeed (API) | ✅ | ⬜ |
| TurkTorrent | ✅ | ⬜ |
| U2 | ✅ | ⬜ |
| UBits | ✅ | ⬜ |
| Ultrabits (API) | ✅ | ⬜ |
| UltraHD | ✅ | ⬜ |
| Unbreakable | ✅ | ⬜ |
| Unlimitz | ✅ | ⬜ |
| upload.cx (API) | ✅ | ✅ |
| Upscale Vault (API) | ✅ | ⬜ |
| UTOPIA (API) | ✅ | ⬜ |
| Vault network | ✅ | ⬜ |
| VC-Lib | ✅ | ⬜ |
| VietMediaF | ✅ | ⬜ |
| White Angel | ✅ | ⬜ |
| WinterSakura | ✅ | ⬜ |
| World-In-HD | ✅ | ⬜ |
| World-of-Tomorrow | ✅ | ⬜ |
| Xingtan (杏坛) | ✅ | ⬜ |
| Xingwan (星湾) | ✅ | ⬜ |
| Xingyung (星陨阁) | ✅ | ⬜ |
| xloli | ✅ | ⬜ |
| xTorrenty | ✅ | ⬜ |
| Xtreme Bytes | ✅ | ⬜ |
| XWT-Classics | ✅ | ⬜ |
| XWtorrents | ✅ | ⬜ |
| YggReborn (API) | ✅ | ⬜ |
| YUSCENE (API) | ✅ | ✅ |
| Zappateers | ✅ | ⬜ |
| Zenith | ✅ | ⬜ |
| ZmPT (织梦) | ✅ | ⬜ |
| ZRPT (自然) | ✅ | ⬜ |

### Semi-private (62)

| Tracker | Built | Live-tested |
|---|:--:|:--:|
| Anime by Belka | ✅ | ⬜ |
| Anime Tosho | ✅ | ⬜ |
| AnimeLayer | ✅ | ⬜ |
| Best-Torrents | ✅ | ⬜ |
| BitMagnet (Local DHT) | ✅ | ⬜ |
| BookTracker | ✅ | ⬜ |
| BootyTape | ✅ | ⬜ |
| comicat | ✅ | ⬜ |
| Deildu | ✅ | ⬜ |
| Devil-Torrents | ✅ | ⬜ |
| DreamingTree | ✅ | ⬜ |
| DXP | ✅ | ⬜ |
| Electro-Torrent | ✅ | ⬜ |
| Ex-torrenty | ✅ | ⬜ |
| ExKinoRay | ✅ | ⬜ |
| EZTVL | ✅ | ⬜ |
| Fenyarnyek-Tracker | ✅ | ⬜ |
| File-Tracker | ✅ | ⬜ |
| Gay-Torrents.net | ✅ | ⬜ |
| HDGalaKtik | ✅ | ⬜ |
| HellTorrents | ✅ | ⬜ |
| HunTorrent | ✅ | ⬜ |
| Hydracker (API) | ✅ | ⬜ |
| Il Corsaro Blu | ✅ | ⬜ |
| ilDraGoNeRo | ✅ | ⬜ |
| Kinorun | ✅ | ⬜ |
| Kinozal | ✅ | ⬜ |
| Kinozal (M) | ✅ | ⬜ |
| Marine Tracker | ✅ | ⬜ |
| Mazepa | ✅ | ⬜ |
| Metal Tracker | ✅ | ⬜ |
| MioBT | ✅ | ⬜ |
| MuseBootlegs | ✅ | ⬜ |
| MVGroup Forum | ✅ | ⬜ |
| MVGroup Main | ✅ | ⬜ |
| NetHD | ✅ | ⬜ |
| New-Team | ✅ | ⬜ |
| NewStudioL | ✅ | ⬜ |
| NoNaMe ClubL | ✅ | ⬜ |
| Polskie-Torrenty | ✅ | ⬜ |
| PornoLab | ✅ | ⬜ |
| Postman (API) | ✅ | ⬜ |
| ProPorno | ✅ | ⬜ |
| PussyTorrents | ✅ | ⬜ |
| Rainbow Tracker | ✅ | ⬜ |
| RGFootball | ✅ | ⬜ |
| RinTor | ✅ | ⬜ |
| RiperAM | ✅ | ⬜ |
| RockBox | ✅ | ⬜ |
| RUDUB | ✅ | ⬜ |
| Rustorka | ✅ | ⬜ |
| seleZen | ✅ | ⬜ |
| Sk-CzTorrent | ✅ | ⬜ |
| SkTorrent.org | ✅ | ⬜ |
| themixingbowl | ✅ | ⬜ |
| TorrentMasters | ✅ | ⬜ |
| TR4KER | ✅ | ⬜ |
| TrahT | ✅ | ⬜ |
| TribalMixes | ✅ | ⬜ |
| Union Fansub | ✅ | ⬜ |
| UzTracker | ✅ | ⬜ |
| Ztracker | ✅ | ⬜ |

### Public (85)

| Tracker | Built | Live-tested |
|---|:--:|:--:|
| 0Magnet | ✅ | ⬜ |
| 1337x | ✅ | ⬜ |
| 52BT | ✅ | ⬜ |
| ACG.RIP | ✅ | ⬜ |
| AniRena | ✅ | ⬜ |
| AniSource | ✅ | ⬜ |
| Bangumi Moe | ✅ | ⬜ |
| BigFANGroup | ✅ | ⬜ |
| BlueRoms | ✅ | ⬜ |
| BT.etree | ✅ | ⬜ |
| BTdirectory | ✅ | ⬜ |
| Byrutor | ✅ | ⬜ |
| Catorrent | ✅ | ⬜ |
| CrackingPatching | ✅ | ⬜ |
| DaMagNet | ✅ | ⬜ |
| dmhy | ✅ | ⬜ |
| E-Hentai | ✅ | ⬜ |
| EBookBay | ✅ | ⬜ |
| Elitetorrent-wf | ✅ | ⬜ |
| ExtraTorrent.st | ✅ | ⬜ |
| EZTV | ✅ | ⬜ |
| FileMood | ✅ | ⬜ |
| Free JAV Torrent | ✅ | ⬜ |
| GamesTorrents | ✅ | ⬜ |
| Internet Archive | ✅ | ⬜ |
| kickasstorrents.to | ✅ | ⬜ |
| kickasstorrents.ws | ✅ | ⬜ |
| LimeTorrents | ✅ | ⬜ |
| LinuxTracker | ✅ | ⬜ |
| Mac Torrents Download | ✅ | ⬜ |
| Magnet Cat | ✅ | ⬜ |
| MagnetDownload | ✅ | ⬜ |
| Magnetz | ✅ | ⬜ |
| MegaPeer | ✅ | ⬜ |
| Mikan | ✅ | ⬜ |
| MixtapeTorrent | ✅ | ⬜ |
| MyPornClub | ✅ | ⬜ |
| nekoBT | ✅ | ⬜ |
| NewStudio | ✅ | ⬜ |
| Nipponsei | ✅ | ⬜ |
| NoNaMe Club | ✅ | ⬜ |
| Nyaa.si | ✅ | ⬜ |
| OneJAV | ✅ | ⬜ |
| OpenSharing | ✅ | ⬜ |
| PandaCD | ✅ | ⬜ |
| PC-torrent | ✅ | ⬜ |
| plugintorrent | ✅ | ⬜ |
| PornoTorrent | ✅ | ⬜ |
| PornRips | ✅ | ⬜ |
| Postman | ✅ | ⬜ |
| RinTor.NeT | ✅ | ⬜ |
| RuTor | ✅ | ⬜ |
| RuTracker.RU | ✅ | ⬜ |
| Sexy-Pics | ✅ | ⬜ |
| Shana Project | ✅ | ⬜ |
| showRSS | ✅ | ⬜ |
| SkidrowRepack | ✅ | ⬜ |
| sosulki | ✅ | ⬜ |
| sukebei.nyaa.si | ✅ | ⬜ |
| The Pirate Bay | ✅ | ⬜ |
| TheRARBG | ✅ | ⬜ |
| Tokyo Toshokan | ✅ | ⬜ |
| Torrent Downloads | ✅ | ⬜ |
| Torrent Oyun indir | ✅ | ⬜ |
| torrent-pirat | ✅ | ⬜ |
| torrent.by | ✅ | ⬜ |
| Torrent9 | ✅ | ⬜ |
| Torrent[CORE] | ✅ | ⬜ |
| TorrentDownload | ✅ | ⬜ |
| TorrentGalaxyClone | ✅ | ⬜ |
| TorrentKitty | ✅ | ⬜ |
| TorrentProject2 | ✅ | ⬜ |
| Torrentsome | ✅ | ⬜ |
| Torrenttip | ✅ | ⬜ |
| U2P | ✅ | ⬜ |
| U3C3 | ✅ | ⬜ |
| Uindex | ✅ | ⬜ |
| VST Torrentz | ✅ | ⬜ |
| VSTHouse | ✅ | ⬜ |
| VSTorrent | ✅ | ⬜ |
| World-torrent | ✅ | ⬜ |
| XXXClub | ✅ | ⬜ |
| xxxtor | ✅ | ⬜ |
| YTS | ✅ | ⬜ |
| Zamunda RIP | ✅ | ⬜ |

## Don't see yours?

[Open an issue](https://github.com/autobrr/harbrr/issues/new) and describe the tracker — if it's
a Cardigann definition it may already work; if it needs a native driver, it joins the planned list
above.

---

*This page is generated by `scripts/gencoverage` from the embedded definitions. To refresh
it: `go run ./scripts/gencoverage > website/docs/coverage.md`.*
