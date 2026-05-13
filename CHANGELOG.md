# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/).
















## [0.3.0](https://github.com/rvben/vedetta/compare/v0.2.14...v0.3.0) - 2026-05-13

### Added

- **storage**: add storage-management API, UI, and serialized file-op lock ([c918733](https://github.com/rvben/vedetta/commit/c91873375bc58e51cdf0886b38ef3f8896a75773))
- **api**: link /storage.html from top + bottom navs ([9d4002a](https://github.com/rvben/vedetta/commit/9d4002a62108ae972c91b32b42695c77ec129add))
- **api**: add storage page logic ([5cdbcf1](https://github.com/rvben/vedetta/commit/5cdbcf13b7866508aae580ca750aca94caa2bf0c))
- **api**: add /storage.html scaffold ([db8ec61](https://github.com/rvben/vedetta/commit/db8ec613ae006941db741a0bedad44703a0f2a3e))
- **api**: implement POST /api/storage/cleanup and GET /api/storage/audit ([508bd94](https://github.com/rvben/vedetta/commit/508bd946e90e2644ec17839c636ea80ca64a6df4))
- **api**: implement POST /api/storage/delete with audit + lock contention handling ([8165a8d](https://github.com/rvben/vedetta/commit/8165a8d1d55ca31b9b9f4d66e346611b00eff2e7))
- **api**: implement GET /api/storage ([c68c04b](https://github.com/rvben/vedetta/commit/c68c04be61256115c555956a38da37307ec9dbe7))
- **api**: add /api/storage routes to OpenAPI schema ([2460661](https://github.com/rvben/vedetta/commit/24606616e326a706b40ebffd5cd2093711162b1a))
- **recording**: add TryRunCleanupAsync and audit helpers ([b018ac5](https://github.com/rvben/vedetta/commit/b018ac595ea2d260a100256678c1293963ca77ac))
- **recording**: implement clips, all, and free_bytes delete targets ([1a62bc7](https://github.com/rvben/vedetta/commit/1a62bc7dd6dd64cc22c374778b8dd11b766d4273))
- **recording**: implement DeleteStorage for segments target ([1828c41](https://github.com/rvben/vedetta/commit/1828c41308a3046515f535d16fc29dd0ae681544))
- **recording**: add StorageBreakdown with per-FS accounting ([812fe39](https://github.com/rvben/vedetta/commit/812fe396701303d1b27d3609d4491af99f52873b))
- **recording**: declare storage management types ([d17465b](https://github.com/rvben/vedetta/commit/d17465b9d5e096928c0b65df023137f57a2d35a0))
- **storage**: add storage_audit table for tracking manual deletions ([87dd13f](https://github.com/rvben/vedetta/commit/87dd13f555b3bdb44cae3ac5159ef1fa980fdbcf))
- **recording**: add SaveEventSnapshot serialized via segmentOpMu ([6738852](https://github.com/rvben/vedetta/commit/673885240055a45e5664def64758dd973c167ee8))
- **storage**: add clip query helpers with COALESCE for nullable end_time ([20d9a46](https://github.com/rvben/vedetta/commit/20d9a4670f8d93af34da9314b733a6bcec707da1))
- **storage**: add segment query helpers for scoped deletion ([487d88c](https://github.com/rvben/vedetta/commit/487d88c01b521fca379dd310a92f559fd96e25e4))
- **recording**: add CurrentSegmentPaths exposing active writers ([7845c85](https://github.com/rvben/vedetta/commit/7845c855310f38757061e4671c2ce87d62035e83))
- **media**: expose Camera() and CurrentSegmentPath() on RecordingConsumer ([943c436](https://github.com/rvben/vedetta/commit/943c436e0508244298ecd010076a6f797f64dc96))

### Fixed

- **recording**: propagate open-segment protection in target=all deletes ([c981bf1](https://github.com/rvben/vedetta/commit/c981bf12dfa973730c1e0062d8f1b18f09420a21))

## [0.2.14](https://github.com/rvben/vedetta/compare/v0.2.13...v0.2.14) - 2026-05-11

### Fixed

- **webrtc**: keep m=audio in BUNDLE so ICE candidates land on an active m-line ([5b1cc1b](https://github.com/rvben/vedetta/commit/5b1cc1b0b79e5ab61fbb6f10649954892e07d7c7))

## [0.2.13](https://github.com/rvben/vedetta/compare/v0.2.12...v0.2.13) - 2026-05-11

### Fixed

- **webrtc**: cap answer level_idc at 3.1 so Chrome can allocate decoder ([44152d6](https://github.com/rvben/vedetta/commit/44152d647ad287db7af2797570e39d37b4c1791d))

## [0.2.12](https://github.com/rvben/vedetta/compare/v0.2.11...v0.2.12) - 2026-05-11

### Fixed

- **webrtc**: sniff in-band SPS/PPS so cameras without sprop-parameter-sets negotiate the right profile ([2ad06b0](https://github.com/rvben/vedetta/commit/2ad06b006a823d167d8b56e7426f50068042cc94))

## [0.2.11](https://github.com/rvben/vedetta/compare/v0.2.10...v0.2.11) - 2026-05-11

### Fixed

- **webrtc**: clear marker bit on SPS/PPS so Chrome assembles full access unit ([117eb82](https://github.com/rvben/vedetta/commit/117eb82fd5c3793953cec8b591ba3db01717ca4e))

## [0.2.10](https://github.com/rvben/vedetta/compare/v0.2.9...v0.2.10) - 2026-05-11

### Fixed

- **webrtc**: default to sub-stream so Chrome can decode ([f482025](https://github.com/rvben/vedetta/commit/f482025315850845c8b3c9973ca3bdf05da46ca3))

## [0.2.9](https://github.com/rvben/vedetta/compare/v0.2.8...v0.2.9) - 2026-05-11

### Fixed

- **webrtc**: advertise rtcp-fb in answer SDP so Chrome decoder commits ([38fc49b](https://github.com/rvben/vedetta/commit/38fc49b568f74780f66fcb74daf8da66e661bf1d))

## [0.2.8](https://github.com/rvben/vedetta/compare/v0.2.7...v0.2.8) - 2026-05-11

### Fixed

- **webrtc**: rewrite answer profile-level-id to match camera bitstream ([9281a60](https://github.com/rvben/vedetta/commit/9281a6016e733c6706491d0d993d2385ca8cf40b))

## [0.2.7](https://github.com/rvben/vedetta/compare/v0.2.6...v0.2.7) - 2026-05-11

### Fixed

- **webrtc**: refragment oversized FU-A and disable NACK responder ([7a182ef](https://github.com/rvben/vedetta/commit/7a182efc2dcaab98f1ac3d35d31f7cf06d2f7b04))

## [0.2.6](https://github.com/rvben/vedetta/compare/v0.2.5...v0.2.6) - 2026-05-11

### Fixed

- **camera**: derive Online from frame freshness, not raw RTSP Connected() ([2116259](https://github.com/rvben/vedetta/commit/21162590f488cc646500260fc7fcbb34adcbae7d))

## [0.2.5](https://github.com/rvben/vedetta/compare/v0.2.4...v0.2.5) - 2026-05-11

### Fixed

- **api**: snapshot endpoint serves live frames with no-cache headers ([3816adc](https://github.com/rvben/vedetta/commit/3816adc46ef662d86aac15732fd5249b809a1c4d))

## [0.2.4](https://github.com/rvben/vedetta/compare/v0.2.3...v0.2.4) - 2026-05-08

### Added

- **stream**: allow ?quality=low to route MSE to the detect substream ([07af2dc](https://github.com/rvben/vedetta/commit/07af2dc06aae0b6458dc3b00d8603b39d64bf8d8))
- **web**: clamp name popover and show click-to-start on autoplay block ([b4d7970](https://github.com/rvben/vedetta/commit/b4d79702f3a69df6e922e6f9655428678078d3a6))
- **api**: expose source_fps in camera detail response ([309ea14](https://github.com/rvben/vedetta/commit/309ea14b452e0f35b8a8c335d786ba2e0426fb23))

### Performance

- **stream**: halve MSE fragment cadence and normalize sample durations ([5273bd7](https://github.com/rvben/vedetta/commit/5273bd7c45667be2607bfbdafb72fa9abad7d8cd))

## [0.2.3](https://github.com/rvben/vedetta/compare/v0.2.2...v0.2.3) - 2026-05-08

### Fixed

- **web**: drift-correct MSE playback to smooth out live preview ([d739707](https://github.com/rvben/vedetta/commit/d7397073526d3ec871cf44f43ce08dec05a89997))

## [0.2.2](https://github.com/rvben/vedetta/compare/v0.2.1...v0.2.2) - 2026-05-08

### Added

- **web**: click a tracked object on the live view to name it ([1c3e3e0](https://github.com/rvben/vedetta/commit/1c3e3e09c6e60463f8375ff95ab9df2723ce1ce7))
- **api**: add POST /api/cameras/{name}/objects to name a live track ([8c1dcd4](https://github.com/rvben/vedetta/commit/8c1dcd4484fa7f18e96ac64e1ebad511ce249484))
- **main**: push matched object names back to the camera overlay ([b613eed](https://github.com/rvben/vedetta/commit/b613eed4ce3e7e55a589f48fbf6cca1e081b1932))
- **camera**: track per-object display names and expose live frame ([27c05fa](https://github.com/rvben/vedetta/commit/27c05fa94693ca3b16b21f5ca83bb928253fff5e))

### Fixed

- **stream**: batch fMP4 fragments and stabilize live-edge auto-seek ([6c6af5d](https://github.com/rvben/vedetta/commit/6c6af5d057eeb7eafb31e4f3cec76bcb59fda97a))
- **camera**: age tracks every frame so events resume after quiet periods ([e340981](https://github.com/rvben/vedetta/commit/e34098190c7e6aafdd3549f89a3712a358fab887))
- **detect**: preserve stationary tracks across motion-gated quiet periods ([3447041](https://github.com/rvben/vedetta/commit/3447041d4123a8292e80b2a342a9dac520f3e1e6))
- **media**: rewrite fMP4 trim/concat to handle multi-track moofs ([668c025](https://github.com/rvben/vedetta/commit/668c025be1a549e13ef228acea929120860ee955))

## [0.2.1](https://github.com/rvben/vedetta/compare/v0.2.0...v0.2.1) - 2026-05-02

### Added

- **web**: pick a tracked-object thumbnail from sightings or references ([e3d7892](https://github.com/rvben/vedetta/commit/e3d7892a7f0b91d811ca1c5ae6b96115490227bd))
- **web**: redesigned tracked objects page ([1d0064a](https://github.com/rvben/vedetta/commit/1d0064a8fdbb7c495461660514dcf4b565c3f013))
- **web**: mask RTSP credentials in camera settings with reveal toggle ([a78eec4](https://github.com/rvben/vedetta/commit/a78eec4a4c1d9df2b627aec087b47d792849d9e4))
- **web**: camera page live view UX improvements ([b473ff6](https://github.com/rvben/vedetta/commit/b473ff62ff3c7b5729bb1480f25320fc904a942f))
- **web**: add tile-density selector to dashboard ([c2c3f11](https://github.com/rvben/vedetta/commit/c2c3f1120c972c3c2fafb6669c3cedec461bc994))
- **web**: auto-fill camera grid layout ([f309a25](https://github.com/rvben/vedetta/commit/f309a25cefb75a4552f82b1bf6e39cc5e11b8f09))
- **web**: cross-cutting UX improvements (W2.4) ([ad60c86](https://github.com/rvben/vedetta/commit/ad60c86344d96beb5749f1db537c3693a4f78032))
- **web**: fix event-card badge legibility and add badge legend ([17991ee](https://github.com/rvben/vedetta/commit/17991eeec5d548512ca33abe01f7de09f9278cc0))
- **web**: replace cryptic identity codes with readable labels on event tiles ([b5d5256](https://github.com/rvben/vedetta/commit/b5d525677086d40c1675b891487f9a8481018448))
- **web**: eager-load camera snapshots on dashboard tiles ([953baf7](https://github.com/rvben/vedetta/commit/953baf78675b02869e9d1fe029a3b0d3b23ddb53))
- **web**: enrich PWA manifest with id, description, screenshots ([8a787c5](https://github.com/rvben/vedetta/commit/8a787c5aab66ac9d69dbb0916b54afd6a093bae8))
- **web**: add PWA chrome to login page ([6769c44](https://github.com/rvben/vedetta/commit/6769c4458364d78298e70704b3b81f60ef6be085))
- **api**: redirect extensionless paths and serve app-shell 404 ([6a32fb5](https://github.com/rvben/vedetta/commit/6a32fb575177c7df219024457bef28d0edb08d74))
- **web**: add sticky jump nav to settings page ([1dd214c](https://github.com/rvben/vedetta/commit/1dd214c7ccbc9459bb77427168e8ff54e62c1782))
- **web**: bump mobile touch targets and landscape camera layout ([9242bc1](https://github.com/rvben/vedetta/commit/9242bc1a7efa07c2df5bc30c11860d48b8823151))
- **web**: live bounding-box overlay on camera view ([b4d5b2e](https://github.com/rvben/vedetta/commit/b4d5b2e183a93cd1eec087a10beda59f859dd188))
- **web**: RTSP test-connection button in camera forms ([f01aca0](https://github.com/rvben/vedetta/commit/f01aca0295ec9563660bb6f10892b0eb2175794d))
- **web**: UI/UX pass across dashboard, streaming, mobile, and PWA ([3138f9e](https://github.com/rvben/vedetta/commit/3138f9e76cebc1e2c17e19cdcb916d62cb2d78d4))

### Fixed

- **mqtt**: assign client before connect to avoid OnConnect race ([de52000](https://github.com/rvben/vedetta/commit/de520008c7b7b19486b11a1e5987e462499a89e7))
- **web**: auto-regenerate missing tracked-object crops from recent sightings ([55848ca](https://github.com/rvben/vedetta/commit/55848caea423012d7924085b716f064148d77530))
- **web**: tracked-object thumbnails fall back when crop file missing ([b0fa986](https://github.com/rvben/vedetta/commit/b0fa986a54dbbf0ae4264c6c44ac0c9923ea3e15))
- **web**: camera events badge shows true total via X-Total-Count ([695f469](https://github.com/rvben/vedetta/commit/695f4695422f1bb8fb27169bfbcede6d70753264))
- **web**: cap camera-page events at 24 with View all link ([114254e](https://github.com/rvben/vedetta/commit/114254ea6bfc1baf825f482717d3a6ae59190647))
- **web**: proceed optimistically on non-404 camera validation errors ([fbbfbaa](https://github.com/rvben/vedetta/commit/fbbfbaa3cfdb6e8ebeab196524626232c83be428))
- **web**: prevent event clip player from collapsing before metadata loads ([9f8855f](https://github.com/rvben/vedetta/commit/9f8855f52917e7973d712b81a97ef61c8e051e53))
- **web**: move notifications card styles out of body innerHTML ([8fe4456](https://github.com/rvben/vedetta/commit/8fe44562c891afc693597d308d629872dcf1d3e1))

## [0.2.0](https://github.com/rvben/vedetta/compare/v0.1.0...v0.2.0) - 2026-04-19

### Added

- **events**: fall back to local disk for snapshots when primary volume is full ([b94761e](https://github.com/rvben/vedetta/commit/b94761e2d86e19cc3a18fa91e75272dfe7d462f6))
- **mqtt**: publish disk status and recording-paused sensors with HA discovery ([5da2bc1](https://github.com/rvben/vedetta/commit/5da2bc1872ee2ed27845c3184ef72a9f2c60b291))
- **recording**: configurable recompress interval and size-priority selection ([c82f956](https://github.com/rvben/vedetta/commit/c82f95664143b49ae46dffdfd924c47c560895c5))
- **recording**: support per-camera retain_days override ([2468cff](https://github.com/rvben/vedetta/commit/2468cff818846c70788a779dceac4f507fcfc99f))
- **recording**: add floor-breaking emergency cleanup to prevent silent recording pause ([c1dd3a3](https://github.com/rvben/vedetta/commit/c1dd3a3539c544e6c20be9fa6406f96b62412c4c))
- **media**: make disk-free threshold configurable and dynamic ([9f07611](https://github.com/rvben/vedetta/commit/9f07611407d773190841d5bd97923cc8bc32a2e0))
- **storage**: add queries for emergency cleanup floor and size-ordered recompress candidates ([f084557](https://github.com/rvben/vedetta/commit/f084557e7f59d96d7afab95d8f0b0715463e7844))
- **config**: add min_disk_free, urgent_cleanup, tiered_storage interval/priority, per-camera retain_days ([0708cd4](https://github.com/rvben/vedetta/commit/0708cd4c27fa1ddc676c3638ee32f6ea244e11ea))
- **config**: add notifications.vapid_subscriber config field ([b0348c4](https://github.com/rvben/vedetta/commit/b0348c4cbbae451d0f47370d72393ca4977b935e))
- **notify**: derive friendly camera names for notification titles ([ff19099](https://github.com/rvben/vedetta/commit/ff190992c48e8c20c32aa96fcd02b3e09666f3d7))
- **api**: add public GET /api/push/snapshot/{id} handler ([3230ecd](https://github.com/rvben/vedetta/commit/3230ecd3983415535fc3f489da8e92d8b59cf9f8))
- **notify**: use signed URLs in push payload image field ([32d64cd](https://github.com/rvben/vedetta/commit/32d64cd33c0608941a7dbadc9dd12c207a41d4e2))
- **notify**: add HMAC-signed snapshot URL signer ([63145d5](https://github.com/rvben/vedetta/commit/63145d5dd5d47ca58955cc8e792cf8d57e5a39ca))
- **ui**: add Notifications section with Enable, prefs matrix, devices list ([669c985](https://github.com/rvben/vedetta/commit/669c985424ce20ebfd7be5d35a7a411a7220e24e))
- **ui**: register service worker, install hint, Remember-me promotion ([d48d7d5](https://github.com/rvben/vedetta/commit/d48d7d56e94d090df437ec5a239109f75cf712c6))
- **ui**: add PWA manifest link to every HTML page ([c24689c](https://github.com/rvben/vedetta/commit/c24689cee433f53da39ff5b4a9551758b5425913))
- **ui**: add PWA manifest and service worker ([4b41233](https://github.com/rvben/vedetta/commit/4b41233928e3064f4eaf2545ec03ef3c832f7324))
- **ui**: add PWA app icons ([a6ae5ff](https://github.com/rvben/vedetta/commit/a6ae5ff9cdbfcbf7d0a31647523aa4ca399c1113))
- **api**: expose notify metrics through /metrics ([38e1d44](https://github.com/rvben/vedetta/commit/38e1d4469dda047306d7136f4faba164057a0058))
- **vedetta**: enqueue events to push dispatcher after snapshot save ([c2c12fd](https://github.com/rvben/vedetta/commit/c2c12fda2211dd381c3f0b9d1783ab4f7778aebc))
- **vedetta**: instantiate notification dispatcher at startup ([fa3f108](https://github.com/rvben/vedetta/commit/fa3f108bf2266e23d77343923e211f0937785f2c))
- **api**: register /api/push/* routes on server mux ([8199925](https://github.com/rvben/vedetta/commit/81999258d4602664d6f4711559dfccc4690936e3))
- **api**: add push subscription, prefs, and test handlers ([5a06298](https://github.com/rvben/vedetta/commit/5a0629849b58fa9677708b7379bbb8ed2f1c8e0f))
- **notify**: add WebPushSender production adapter ([e8e10c7](https://github.com/rvben/vedetta/commit/e8e10c710fdc3e68106adedc940e61b2ecbd10e3))
- **notify**: scaffold NotificationDispatcher with Store interface ([3c7ded0](https://github.com/rvben/vedetta/commit/3c7ded0186a79b6c5e5ce71957362764ab984a5f))
- **notify**: add lock-free metrics counters ([318e5d4](https://github.com/rvben/vedetta/commit/318e5d4b4d04f624da02cf2630e7881b47e72eed))
- **notify**: add push payload builder ([c6ee1c8](https://github.com/rvben/vedetta/commit/c6ee1c86ba7b961816e201dad21896f9797c2216))
- **notify**: add CooldownCache with injectable clock ([00853f8](https://github.com/rvben/vedetta/commit/00853f83f94fe60a7d7ba5f0e3079388e66548f5))
- **notify**: add subscription endpoint and key validation ([6c98cd8](https://github.com/rvben/vedetta/commit/6c98cd88389227ec1f27e8791575b3d39a4eb374))
- **notify**: add VAPID keypair load/generate with fail-closed policy ([164f43d](https://github.com/rvben/vedetta/commit/164f43de98ef965a68f869e56a8aba89c412a67e))
- **storage**: add notification_prefs CRUD with default-enabled semantics ([6fd3b7a](https://github.com/rvben/vedetta/commit/6fd3b7a2a4dac2a2225681e055c7944d931ed379))
- **storage**: add push_subscriptions CRUD ([8fcf243](https://github.com/rvben/vedetta/commit/8fcf2431c3ab36ffd959d03ce0307301987ce61f))
- **storage**: add push_subscriptions and notification_prefs tables ([ebe2ed4](https://github.com/rvben/vedetta/commit/ebe2ed45fbf55586f8f498cafbc570c0247b0c03))
- **storage**: add steady-state and linear projection to StorageStats ([eae759f](https://github.com/rvben/vedetta/commit/eae759f67bf76fe8db98e33c46576209f3add031))
- **codecs**: auto-install OpenH264 on startup when missing ([843f123](https://github.com/rvben/vedetta/commit/843f12327a66a8cc82826937b79886d40f10a84c))
- **health**: expose detection subsystem status in /api/health ([4d14796](https://github.com/rvben/vedetta/commit/4d14796699b044567d3d1bdb0c0f94ccde1bde30))
- **ui**: add stop/start toggle on dashboard camera cards ([10a42ff](https://github.com/rvben/vedetta/commit/10a42ffca4fb79246e0bf7f1d9555c79a2c23800))
- **mqtt**: publish 'stopped' status for stopped cameras ([4b58218](https://github.com/rvben/vedetta/commit/4b5821886126c5c0b1e901f955415f2db9b9110a))
- **api**: add stop/start camera endpoints with OpenAPI spec ([22879cd](https://github.com/rvben/vedetta/commit/22879cdbb5d9b32ee1be5e0cbd9f31e45cc12fcb))
- skip stopped cameras on startup ([608dcd6](https://github.com/rvben/vedetta/commit/608dcd6161445a5066fc1628bd1572eeec056879))
- **recording**: per-camera stop/start recording ([6e66f0d](https://github.com/rvben/vedetta/commit/6e66f0d4d03d287bc8dd2be0a2f63cdf9c8b5071))
- **camera**: per-camera context with StopCamera/StartCamera/IsStopped ([75a809e](https://github.com/rvben/vedetta/commit/75a809e27bb1c49576264ba867654feb57613545))
- **storage**: add camera stopped state persistence via kv_store ([7fc741c](https://github.com/rvben/vedetta/commit/7fc741c5608661e94f665a9e6cd2655092b6a243))
- **mqtt**: publish object counts and event end messages ([90417ff](https://github.com/rvben/vedetta/commit/90417ff2820527f41375fda8a9eb92dff44703f4))
- **mqtt**: add PublishObjectCount for per-camera per-label object counts ([27571d0](https://github.com/rvben/vedetta/commit/27571d059616129d400186208d9883553cd43d33))
- **ui**: add camera management page with CRUD operations ([bdfe140](https://github.com/rvben/vedetta/commit/bdfe140628c3c90d14d8a09b462f2046d7cce042))
- **ui**: add recording, detection, and password cards to settings page ([2e5b971](https://github.com/rvben/vedetta/commit/2e5b9715ee1d4120e4d9ce2a838f2e47f6eaf86b))
- **api**: add camera management CRUD endpoints ([f115070](https://github.com/rvben/vedetta/commit/f1150709e97f7b873b15f49989bf48735713391c))
- **api**: add password change and auth info endpoints ([771585a](https://github.com/rvben/vedetta/commit/771585a81d2f0e5e2213dfd2d2b2918ffd79fde6))
- **api**: add recording and detection settings endpoints with hot-reload ([c09c3a0](https://github.com/rvben/vedetta/commit/c09c3a0c4ec1efbd927aed769066eb41ee5ae04b))
- **api**: wire detector and recording config into server ([527426e](https://github.com/rvben/vedetta/commit/527426e39132ddbc49dbb029a6e9af5bc22833fc))
- **config**: add camera update/remove and auth password config writers ([e13856a](https://github.com/rvben/vedetta/commit/e13856abe16ee7f735eb5d9d479817917691bc31))
- **detect**: add SetScoreThreshold and SetLabels for hot-reload ([df1fee3](https://github.com/rvben/vedetta/commit/df1fee3fe70c3a55a5b154efdde3b19d8263bf4b))
- **config**: add UpdateRecording and UpdateDetect config writers ([027cb30](https://github.com/rvben/vedetta/commit/027cb30cb10b28fa1c1147a45cbe774f59b355bf))
- **ui**: add Settings link to navigation across all pages ([f02327e](https://github.com/rvben/vedetta/commit/f02327e21848571e0943a64751ff6be99cca53f2))
- **ui**: add settings page with MQTT config and update checker UI ([0cf9ceb](https://github.com/rvben/vedetta/commit/0cf9ceb947aaccd67e36a302ec2422a64ee8d2b8))
- **api**: show update badge on system page when new version available ([dd76387](https://github.com/rvben/vedetta/commit/dd76387ec29f312be38d11c5a4a1af416dfe2d0c))
- **api**: add settings endpoints for MQTT config and update checking ([d43727f](https://github.com/rvben/vedetta/commit/d43727fcf6e9e8eec2701750a8189bb5f42a09e2))
- wire update checker into startup and server ([f709ab7](https://github.com/rvben/vedetta/commit/f709ab7fdad6a94195aad53e19cb4ba2a329896f))
- **config**: add UpdateMQTT and UpdateUpdates for live config writes ([f272043](https://github.com/rvben/vedetta/commit/f2720431b36ed09218d091bd9e745bf158866fad))
- **mqtt**: add mDNS broker discovery via zeroconf ([9946f33](https://github.com/rvben/vedetta/commit/9946f3340f369191cb893280cbb48c95c79cfc42))
- **update**: add GitHub release checker with semver comparison ([d7aec4c](https://github.com/rvben/vedetta/commit/d7aec4c003fbd4d2a7f9ad4d51a32b0c7eac8df7))
- **config**: add updates section for update checker settings ([6f94eca](https://github.com/rvben/vedetta/commit/6f94eca90085d1580c42bc0c9bc8fb2d29beabea))
- **storage**: add kv_store table for persistent settings ([4fbae12](https://github.com/rvben/vedetta/commit/4fbae1207ef4f24743d47d437af1e5b60b2a80fc))
- **ui**: harden frontend with action whitelisting and setup token flow ([96c91ad](https://github.com/rvben/vedetta/commit/96c91add1511cf02af5e48a9012e805e4a27a717))
- **api**: add setup token security, body limits, CSP headers, and version embedding ([641199d](https://github.com/rvben/vedetta/commit/641199dd3672d9edad6373d35e9f50f99583ba70))
- add config validation, auth token scoping, and recording improvements ([f566525](https://github.com/rvben/vedetta/commit/f566525cd3ffe01a22e97829787b7247dd60e239))
- **api**: wire ServerInterface for compile-time API contract enforcement ([95b7680](https://github.com/rvben/vedetta/commit/95b7680c5b2e39ba431dc969060b536e76fcb55c))
- **api**: add GET /api/tokens endpoint for listing API tokens ([f556396](https://github.com/rvben/vedetta/commit/f556396165144cdabb142db014c04d811e9d1a54))
- **api**: add object and streaming endpoints to OpenAPI spec ([4fd8236](https://github.com/rvben/vedetta/commit/4fd82367e1d23c851a3d2cd2263e154dbbe6693c))
- **api**: add people and face endpoints to OpenAPI spec ([d44bd60](https://github.com/rvben/vedetta/commit/d44bd60570cba3e7c22b1c36d70c7d1e6406f020))
- **api**: add recording endpoints to OpenAPI spec, expose segment IDs ([5248938](https://github.com/rvben/vedetta/commit/5248938f40d77061544afb56e8ec36506f5a517a))
- **api**: add event endpoints to OpenAPI spec, add since filter and list envelope ([7e6e8f7](https://github.com/rvben/vedetta/commit/7e6e8f7f6d1e1c5bbf3721b5e21028e0a97b1ae3))
- **api**: add camera endpoints to OpenAPI spec, add GET /api/cameras/{name} ([f06576e](https://github.com/rvben/vedetta/commit/f06576e7b2311ab7dacf026adef8bf0364049d1e))
- **api**: add auth endpoints to OpenAPI spec with contract tests ([23d9c5a](https://github.com/rvben/vedetta/commit/23d9c5ae0ee618a38f5a14c79a8ce23ff511ef44))
- **api**: serve OpenAPI spec at /api/openapi.json with contract test harness ([e10bf20](https://github.com/rvben/vedetta/commit/e10bf20e2c6f72d6c77b51fe0a1f2396e54eeab4))
- **api**: add OpenAPI 3.1 spec skeleton with health endpoints ([412d8b5](https://github.com/rvben/vedetta/commit/412d8b5ca3fde7c823e1b49e7ef1263c9a0a80d9))
- **ui**: improve account modal for proxy and session auth ([2f5830f](https://github.com/rvben/vedetta/commit/2f5830ff238c1719263ec15edaa095ce246bf71a))
- **ui**: hide change password form for proxy-authenticated users ([5367ccf](https://github.com/rvben/vedetta/commit/5367ccf1975bd4a3d366378e594c345532c11082))
- **api**: redirect already-authenticated users away from login page ([2992b21](https://github.com/rvben/vedetta/commit/2992b2178a5fa477db21c144f521fbcd769ede6d))
- **auth**: add debug logging for proxy authentication ([8a335c4](https://github.com/rvben/vedetta/commit/8a335c4ed7b4ffd9459ee40a31aaf5b64d78f304))
- **auth**: add reverse proxy header authentication ([6688898](https://github.com/rvben/vedetta/commit/6688898ff1a2fde3a397ebeeee92b1c42a6db449))
- **config**: add auth.proxy.header config for reverse proxy authentication ([a105e78](https://github.com/rvben/vedetta/commit/a105e783dda0b2d1b345a3039191aec6f2af0f6c))
- **recording**: add manual recompression trigger ([70367a3](https://github.com/rvben/vedetta/commit/70367a34a50772e204ff8b0141e680f80831a513))
- **recording**: expose recompression runtime stats (last_run, segments, bytes_reclaimed) ([030b5f9](https://github.com/rvben/vedetta/commit/030b5f9fa0e5f3f61584cae8f6feba911aa07e4f))
- **recording**: clean stale .tmp files from interrupted recompression on startup ([6fa80a7](https://github.com/rvben/vedetta/commit/6fa80a70755f2f56648983bdc817ee8e63a0039b))
- **recording**: wire up tiered storage recompressor and expose stats ([14291bf](https://github.com/rvben/vedetta/commit/14291bfc8ec10f2f0051c5d412e70761de48e1c6))
- **recording**: add tiered storage recompression job ([20d7019](https://github.com/rvben/vedetta/commit/20d7019ccb296113ba2bf7ee8a9d434c0f2d0636))
- **media**: implement TranscodeSegment for tiered storage recompression ([529763b](https://github.com/rvben/vedetta/commit/529763bc162bdc0b98b1fab9eebae728eb87999c))
- **media**: add resolution check and source resolution reader for transcoder ([de97cbd](https://github.com/rvben/vedetta/commit/de97cbda69f375fd94bc225e46a1b5180d0887a6))
- **media**: add YCbCr scaler for tiered storage transcoding ([341a7e9](https://github.com/rvben/vedetta/commit/341a7e90a074689ddb98f2039d8bcddd0f3cc0f1))
- **media**: track HLS segment paths in use for recompression safety ([a395a6d](https://github.com/rvben/vedetta/commit/a395a6de09ce890a3b8d36b31a54dc6dd81206d6))
- **config**: add TieredStorageConfig with per-camera overrides and schedule parsing ([6d0d5eb](https://github.com/rvben/vedetta/commit/6d0d5eb9b317b0872aa4ec5b8f1eb0f399a94ecb))
- **storage**: add recompression tracking columns to segments ([e3ea738](https://github.com/rvben/vedetta/commit/e3ea738a31f01690d67c3665523e1d9dc246a9e2))
- server-side re-segmenting for HLS playback ([407540b](https://github.com/rvben/vedetta/commit/407540b3091ff0b9a70d11f48181b76dcff3eb97))
- HLS playback for recorded video ([b1c9c6a](https://github.com/rvben/vedetta/commit/b1c9c6a989803b3a624aac7d66b2162bcc637db6))
- **ui**: snap to event start time when clicking event bars on timeline ([6ec2935](https://github.com/rvben/vedetta/commit/6ec29352be716ca415f1e23b1c2f1d9f1d2810bf))
- **ui**: render waveform timeline with motion activity data ([69cc05b](https://github.com/rvben/vedetta/commit/69cc05bc6b4430c915877254688caae12e2d25e0))
- **ui**: add canvas element and CSS for waveform timeline ([a9ca55f](https://github.com/rvben/vedetta/commit/a9ca55f1402c06f5fcb40b6cb144ce9367dbad9d))
- **api**: add motion activity and event end_time to timeline response ([bf3ef76](https://github.com/rvben/vedetta/commit/bf3ef76a97fd919aa94ae01ee493a6f1cbbbfe61))
- wire motion activity channel from cameras to DB ([2755e56](https://github.com/rvben/vedetta/commit/2755e566064893e860b3d68171ed699429b07a5a))
- **recording**: add motion activity retention cleanup ([3d779f4](https://github.com/rvben/vedetta/commit/3d779f4c87729518cd7ea46b4925824c71c8186e))
- **camera**: add per-minute motion activity accumulation ([42435b8](https://github.com/rvben/vedetta/commit/42435b82a64d4344fe5a0ea440d4387d25187782))
- **storage**: add motion_activity table and methods ([b377672](https://github.com/rvben/vedetta/commit/b3776724e59ce6bf5257e5614662f26fe167b88e))
- **detect**: expose frame coverage from motion detector ([338d5d7](https://github.com/rvben/vedetta/commit/338d5d7e6dcf629d730f0438a625efa6dac90ccd))
- **ui**: YouTube-style video control bar overlay ([92d18ee](https://github.com/rvben/vedetta/commit/92d18ee7ebc91b2ec221077f2dc3562451d77e55))
- **ui**: click video to pause/play with centered indicator ([63354cc](https://github.com/rvben/vedetta/commit/63354ccc4d120ebcff28e35955359f3ec65810e2))
- **ui**: add pause/play button to video overlay ([1d4b27c](https://github.com/rvben/vedetta/commit/1d4b27cf8cc155fb8ca275794994b67a45bec627))
- **ui**: hide stream toolbar, show only during playback ([4182f38](https://github.com/rvben/vedetta/commit/4182f38f00cadec8508c611e495b8384d4b7d303))
- **ui**: move mute/PiP/fullscreen to video hover overlay ([521f25f](https://github.com/rvben/vedetta/commit/521f25fc9661b97cfd5a25ba2bd19c13b189d347))
- **ptz**: add press-and-hold controls and keyboard shortcuts ([76363c2](https://github.com/rvben/vedetta/commit/76363c213584e774cfc05470af6224b934eff6a8))
- **ptz**: probe cameras for PTZ support at startup ([599b69d](https://github.com/rvben/vedetta/commit/599b69d8e16e2d6fce46c7d993ae121dfe410bf5))
- **ptz**: add D-pad and zoom controls UI ([89fc9e1](https://github.com/rvben/vedetta/commit/89fc9e1e1ec89f66886d6641ad4a783d1e62cc60))
- **ptz**: add PTZ API endpoint and camera status field ([3401881](https://github.com/rvben/vedetta/commit/3401881090f8bc140627367e94ff273cf85c3a0e))
- **ptz**: ContinuousMove and Stop SOAP commands ([eb1b920](https://github.com/rvben/vedetta/commit/eb1b9204132c74dc184fc17b80b95a32c60db252))
- **ptz**: ONVIF capability detection with auth fallback ([eaf2f37](https://github.com/rvben/vedetta/commit/eaf2f3716cd5054766ae980a99724eb746036d5f))
- **ptz**: add ONVIF SOAP helpers and WS-Security auth ([1f6748c](https://github.com/rvben/vedetta/commit/1f6748c2aba3c91ebaf6f7454b15f14c1fac86bf))
- **auth**: password change and remember-me sessions ([6e4eabe](https://github.com/rvben/vedetta/commit/6e4eabeafc81b5cdc4dff9a3f026a9200e809e83))
- **auth**: add remember-me with extended session TTL ([e5ed8b7](https://github.com/rvben/vedetta/commit/e5ed8b712f9344f13be496540297eb730cdf6641))
- **auth**: add change password endpoint ([18ff115](https://github.com/rvben/vedetta/commit/18ff115297ce949a4aeae1421fa42b1c03ebd827))
- web-based onboarding flow ([7081a33](https://github.com/rvben/vedetta/commit/7081a336d540c482d52e5d102878c56c09c7b10c))
- **onboarding**: add camera thumbnails and editable names during discovery ([fdb2372](https://github.com/rvben/vedetta/commit/fdb237218c7bd13be51108f52946b95f2dd3f0c1))
- **ui**: empty dashboard state with discover and add-manual CTAs ([7204e7f](https://github.com/rvben/vedetta/commit/7204e7f453fb1a8528814487c91a8955c12608af))
- **ui**: add onboarding setup page with account creation and camera discovery ([2e1b400](https://github.com/rvben/vedetta/commit/2e1b4001d67e025706e2e830edd189f8a4621224))
- **camera**: add hot-add support to camera manager ([eec8462](https://github.com/rvben/vedetta/commit/eec84621a448157cb10012d44ec1b8a177468ddb))
- setup mode startup path in main ([d578eb9](https://github.com/rvben/vedetta/commit/d578eb91dd7c8e7a1479739bff5c4223578aa6c1))
- **api**: add setup mode routing and transition to full mode ([2101e7a](https://github.com/rvben/vedetta/commit/2101e7aa0a390c9af50f367d0714dcdb2ea4d3a0))
- **api**: add setup mode handlers for onboarding ([8d734d1](https://github.com/rvben/vedetta/commit/8d734d10abe83149d7dd49cdc239d9a4a3f74f3d))
- **auth**: seed config users into DB on startup ([eeea997](https://github.com/rvben/vedetta/commit/eeea997b76ac2a50399d28008f1aa2f6d6fe8f0a))
- **auth**: add DB-primary auth with NewFromDB constructor ([fae08b5](https://github.com/rvben/vedetta/commit/fae08b55d8b3827b60588782e26a121ea3820665))
- **storage**: add auth_users table with save, seed, and list ([9a6e2e8](https://github.com/rvben/vedetta/commit/9a6e2e87f92a57d7aec16e07c4960514624c9277))
- **config**: add config file writing with yaml.Node preservation ([109198a](https://github.com/rvben/vedetta/commit/109198a493216462bf806fcdbef5237c1fbea646))
- **config**: add LoadOrDefault and allow zero cameras ([84387b2](https://github.com/rvben/vedetta/commit/84387b26113003db26609d143a6f67243dd9afdc))
- **ui**: unify unmatched faces and appearances into same identification modal ([d347f10](https://github.com/rvben/vedetta/commit/d347f104c378589df4e1d9590538bd030cc9b9cf))
- **api**: draw bounding boxes on-the-fly when serving event snapshots ([d95f9ff](https://github.com/rvben/vedetta/commit/d95f9ffd918b5d5ff67110cd417755e6843795a1))
- **ui**: full-screen identification modal for unidentified appearances ([622a447](https://github.com/rvben/vedetta/commit/622a447353446b365940d03ce2e911f30f8f7729))
- **ui**: dismiss/ignore unidentified appearances ([e1dfee5](https://github.com/rvben/vedetta/commit/e1dfee55a3c007e66f33cc8346c32ebb81184c5c))
- **ui**: inline identify picker for unidentified appearances ([aacd31d](https://github.com/rvben/vedetta/commit/aacd31daa99204a01693b914fcccfc013b85ed32))
- **ui**: unidentified appearances section on People page ([1c62426](https://github.com/rvben/vedetta/commit/1c62426d5a101e0f4d5dd51aef71c1e9ba1a0b89))
- **ui**: show face and appearance counts, use best face for thumbnail ([44ecfcc](https://github.com/rvben/vedetta/commit/44ecfccc99816a3a10cf7ba2721400900fac171e))
- **ui**: show faces and appearances when expanding a person ([4e70b83](https://github.com/rvben/vedetta/commit/4e70b83da45fde4dc439f354d8fea3028e75f3cc))
- **ui**: searchable identify picker replaces button list ([ae47ca3](https://github.com/rvben/vedetta/commit/ae47ca319eba70ec5b5ef7c80a05f4ab8da9fa67))
- **ui**: compact face-chip picker replaces button list for identification ([a2eee8d](https://github.com/rvben/vedetta/commit/a2eee8d68f1ea9a2a7000d876e2bfc4227024067))
- **ui**: interactive detection overlay with click-to-identify ([42e2084](https://github.com/rvben/vedetta/commit/42e20841c1b8f3b471cea9a528b8d99f2fa28844))
- assign person events to existing people from event detail ([ad8687b](https://github.com/rvben/vedetta/commit/ad8687bd2fec44b41203ddb0695a9feb899219d2))
- body re-ID fallback and source event linking for person tracking ([efa7a9d](https://github.com/rvben/vedetta/commit/efa7a9d14b0e0d38fb7b70a8b8faf73a48a8c0b9))
- person tracking creates People entries, event detail reloads after tracking ([2644934](https://github.com/rvben/vedetta/commit/2644934a01e336fa5deb38beb566388062f5ad9b))
- **ui**: detection crop preview on event detail for tracking clarity ([5b170b2](https://github.com/rvben/vedetta/commit/5b170b2e40196849db0ab4eb27483c16e2b9e5a9))
- **ui**: real-time doorbell notifications via Server-Sent Events ([06f5c73](https://github.com/rvben/vedetta/commit/06f5c73ea5b49f95e12cd42c79e92b4286d2d08c))
- ONVIF event subscription for native doorbell detection ([3b841dc](https://github.com/rvben/vedetta/commit/3b841dc91c266dbad041ce8ff6dd08de7a74a67e))
- doorbell webhook with face recognition and MQTT snapshots ([5f1ab0a](https://github.com/rvben/vedetta/commit/5f1ab0ae2d8f67311ff0073df023a3edb9b29d95))
- **mqtt**: publish detection snapshots for HA notifications ([29efd83](https://github.com/rvben/vedetta/commit/29efd83ef78f7676fd6c3e9716fcfa6089ce71f8))
- **face**: set sub_label on events when faces match known people ([d0fb9d8](https://github.com/rvben/vedetta/commit/d0fb9d8837c10445d02401526745e8dd6921fe8b))
- per-object threshold, false positive dismissal, and event object filter ([f8c9e4b](https://github.com/rvben/vedetta/commit/f8c9e4bfbf3f692bcd1f8380187efb307985df6f))
- sub-labels, background re-matching, and configurable threshold ([fa71878](https://github.com/rvben/vedetta/commit/fa7187881f5c41301ac1efec8e2850523fb6f2dd))
- **config**: configurable object match threshold ([f6fa077](https://github.com/rvben/vedetta/commit/f6fa077de32cfa5268446c3ff607d9ccef51dc15))
- **ui**: expert-level objects page, input modals, and event filtering ([cb3e827](https://github.com/rvben/vedetta/commit/cb3e8276c1f68bea10ef06fdd14607a1a9ea15f7))
- async object matching, enriched MQTT presence, and HA discovery ([5d67fa3](https://github.com/rvben/vedetta/commit/5d67fa3c456017a5c243dee94c7592f02d006b04))
- MQTT object sightings, presence publishing, and event gallery badges ([2d46278](https://github.com/rvben/vedetta/commit/2d462787007ff10d5489094595f6d9a801cc250b))
- **ui**: add Objects tab to navigation across all pages ([172b2f0](https://github.com/rvben/vedetta/commit/172b2f0c8f6c797289c8f5873be64f4167d8fbfc))
- objects page, multiple references, and improved event tracking UX ([6c929de](https://github.com/rvben/vedetta/commit/6c929de10bde6780da866bf480faf133824f2d93))
- **onnx**: add ReduceMean operator for OSNet model support ([e7e245a](https://github.com/rvben/vedetta/commit/e7e245a0ab60e6493da8d143dc238c070ed189b3))
- object re-identification — track and recognize specific objects ([7194b85](https://github.com/rvben/vedetta/commit/7194b854dded217111a452858e66a11d6a663011))
- **storage**: add known_objects and object_sightings tables ([0e474fe](https://github.com/rvben/vedetta/commit/0e474fec1bc9503a6a77d9f22b6972d2847e15dd))
- **detect**: add OSNet object re-identification embedder ([25889e8](https://github.com/rvben/vedetta/commit/25889e8b535d9680230319547ac4d34dadcb47ff))
- **face**: auto-cluster unmatched faces into unnamed persons ([aa504ef](https://github.com/rvben/vedetta/commit/aa504ef97964c3f2e926268a0dedfe4efdd3aebc))
- **ui**: auto-refresh camera status badges on dashboard ([0e20c22](https://github.com/rvben/vedetta/commit/0e20c2226201245fe2c19c637cd3aae39614631c))
- wire v1 auth and event pipeline into main loop ([010caca](https://github.com/rvben/vedetta/commit/010cacace0bca5c0ef3a58a3544a19507fa08857))
- **recording**: event metadata retention and media availability reconciliation ([84999c7](https://github.com/rvben/vedetta/commit/84999c7ed83ae33e54753f16d02379cfd49820ba))
- **api**: health probes, metrics, face backfill, and polygon zone endpoints ([4a731a8](https://github.com/rvben/vedetta/commit/4a731a86de09d9672e2e2fad458f5a3054c31301))
- **auth**: session auth with CSRF protection and scoped API tokens ([944273e](https://github.com/rvben/vedetta/commit/944273ea6c16d58291cf66f62c4b9b9ac2d654ca))
- **storage**: auth sessions, API tokens, event availability, and zone polygons ([869f469](https://github.com/rvben/vedetta/commit/869f4696f419fcde435931859eb24e72d23ecf26))
- **camera**: polygon zones, detect toggle, degraded status, and event snapshots ([9a47e28](https://github.com/rvben/vedetta/commit/9a47e288dbbfd6971c7b9c3fba3212f9bc00ce03))
- **detect**: remove runtime downloads, fix ONNX ops, and harden decode pipeline ([7f253a8](https://github.com/rvben/vedetta/commit/7f253a8777ac925dcda206a5b1c8b0e30a17a7ed))
- **face**: wire face recognition pipeline into camera loop with people UI ([ad2d8e1](https://github.com/rvben/vedetta/commit/ad2d8e17eaaccac99a62412fd1d4655d182b27cc))
- **face**: SCRFD + MobileFaceNet face detection and embedding pipeline ([d3e78ba](https://github.com/rvben/vedetta/commit/d3e78bae72a994e208eab8ddbd9602a0889eab89))
- **ui**: zone drawing and management on camera page ([3a56b45](https://github.com/rvben/vedetta/commit/3a56b453399403f4ebe653f2d8ab5df35958f0ad))
- zones with presence tracking, timeline thumbnails, ONNX PRelu/Gemm ops ([3153572](https://github.com/rvben/vedetta/commit/3153572cc7856ee21504ae511d39247f520f22fb))
- **ui**: redesign recordings page with per-camera timeline cards ([af18486](https://github.com/rvben/vedetta/commit/af18486bb302757cd8dde0bab1cdc635399ff48a))
- **api**: add POST /api/events/{id}/clip endpoint for clip re-extraction ([b8c803b](https://github.com/rvben/vedetta/commit/b8c803bb27b43e371e7358a353f651ec14990607))
- **api**: recording export endpoint with segment concat and range trim ([5526a4b](https://github.com/rvben/vedetta/commit/5526a4bcd4ae22672bdcae88681f54f20f6860b8))
- **api**: optional HTTPS/TLS support with config validation ([043f2fd](https://github.com/rvben/vedetta/commit/043f2fdf3022402d6fcf6c2f4ee53d4a52941ee2))
- **api**: graceful HTTP shutdown with hardened server timeouts ([3f4edd0](https://github.com/rvben/vedetta/commit/3f4edd0eaf86ea81ded65ca0d26c06805d50bb60))
- **auth**: add HTTP and RTSP authentication with rate limiting ([8cd182b](https://github.com/rvben/vedetta/commit/8cd182b105864456795e53e759e07790bfe16228))
- **stream**: MSE live streaming over WebSocket with fMP4 ([9e9ed8a](https://github.com/rvben/vedetta/commit/9e9ed8afffee1bbad26d600f11d012971c1d29b0))
- **recording**: disk space monitoring with pause/resume and urgent cleanup ([6ffefa7](https://github.com/rvben/vedetta/commit/6ffefa7b4ef339a48b5f799b298d884f858e666f))
- **detect**: auto-download YOLO model from GitHub releases ([51dd9a2](https://github.com/rvben/vedetta/commit/51dd9a26d795789cc772d685d7fa12ac5d1c8b45))
- **stream**: add UDP transport and sub-stream support to RTSP server ([f143d87](https://github.com/rvben/vedetta/commit/f143d87a24d465b8a8166529560f9587e4719499))
- **stream**: add RTSP re-publishing server for Home Assistant integration ([9e89691](https://github.com/rvben/vedetta/commit/9e89691b1702eacd36275fff29a2ac90d74241e4))
- **ui**: camera page stream controls, audio, fullscreen, and stats overlay ([f01ca71](https://github.com/rvben/vedetta/commit/f01ca71952dd6b581e51d72570049ce01d015093))
- **ui**: auto-start WebRTC live stream when opening camera page ([264fac7](https://github.com/rvben/vedetta/commit/264fac7eec03b9e0db2df0168fc9866d4a9f21c8))
- **ui**: show event duration badge in gallery + add tests ([a0468a6](https://github.com/rvben/vedetta/commit/a0468a632a515d27669800d5d3b0b98c9ff1b243))
- **events**: dynamic event duration with max cap ([62999a6](https://github.com/rvben/vedetta/commit/62999a69ded3586ecffc4cfeb1c6b831747714f3))
- **detect**: add configurable label filtering ([5c37efa](https://github.com/rvben/vedetta/commit/5c37efa61e345e0b1468d0a266963388af68a8ae))
- **events**: add recording link and fix clip extraction timing ([27f7cef](https://github.com/rvben/vedetta/commit/27f7cef3dbd08f0abc68957ed344c1e3f5bc0e45))
- **snapshot**: generate annotated event snapshots with bounding boxes ([9e7f481](https://github.com/rvben/vedetta/commit/9e7f481b7061797f06ee2b621c4d23404a68c76a))
- **deploy**: add log rotation configs for macOS and Linux ([fd97075](https://github.com/rvben/vedetta/commit/fd97075b9a57165a9c73cc5834f760c50697f71b))
- **deploy**: add systemd and launchd service files ([e8b73e4](https://github.com/rvben/vedetta/commit/e8b73e4efa6be9689c2937fba27d74def52220aa))
- **media**: auto-download OpenH264 from Cisco on first run ([d0001df](https://github.com/rvben/vedetta/commit/d0001df913c808d7a1a7ceec8f93128d00e6d23a))
- **media**: replace ffmpeg with native Go media pipeline ([c395cf2](https://github.com/rvben/vedetta/commit/c395cf2e0663098f1d70e03aa6acecce26765809))
- **recordings**: add play and download buttons to recording segments ([1880ff7](https://github.com/rvben/vedetta/commit/1880ff71f7d6c7f81e5a0827be4676d8b04d9287))
- **camera**: cache snapshots to disk for offline camera display ([6ec1ff5](https://github.com/rvben/vedetta/commit/6ec1ff5c55ed7d7a9fa4d36d62b6217029c61c1d))
- **ui**: integrate shortcut modal, search, and date picker into all pages ([dfe1da1](https://github.com/rvben/vedetta/commit/dfe1da1e83c7e0b67ceaff59089b16d475f127ea))
- **ui**: add keyboard shortcut modal, timeline hover, infinite scroll, and search ([a01437b](https://github.com/rvben/vedetta/commit/a01437be0a60482aecd1b04577eb901232276c04))
- **ui**: enhance frontend with theme toggle, birdseye, timeline, and playback ([85a2687](https://github.com/rvben/vedetta/commit/85a26875a91283770f5669d095cb588a0a794313))
- **api**: add endpoints for timeline, calendar, events, and playback ([81632b1](https://github.com/rvben/vedetta/commit/81632b16faf1e4be2062161a19346284a80a81e9))
- **storage**: add query methods and fix errcheck warnings ([764445c](https://github.com/rvben/vedetta/commit/764445c95e02fbd4e1f0f009185d8b76ef59d027))
- **api**: add timeline, calendar, event counts, and pagination endpoints ([96d0a3f](https://github.com/rvben/vedetta/commit/96d0a3fbf0784c09780fd642838bd8facc0fc3ec))
- **ui**: complete frontend overhaul with 7 improvements ([154022a](https://github.com/rvben/vedetta/commit/154022a8f8f313f52985176793067c7e028c315b))
- **mqtt**: publish periodic camera status updates ([3e86e4c](https://github.com/rvben/vedetta/commit/3e86e4c9172488cada9842ca0beeb34b0458fd2b))
- **mqtt**: add Home Assistant discovery, availability, and auto-reconnect ([b2f9a54](https://github.com/rvben/vedetta/commit/b2f9a54ca3aae0061341a24664950486cbdcb150))
- **detect**: dual-backend architecture with optional C ONNX Runtime ([675c21e](https://github.com/rvben/vedetta/commit/675c21e17e65b30e9e3f777992f2db0d718bb535))
- **detect**: add pure Go ONNX runtime inference engine ([cf1ad44](https://github.com/rvben/vedetta/commit/cf1ad44ae6e121edf057905455b847c8984f2938))
- **config**: human-readable max_storage setting and remux temp cleanup ([bf30d1a](https://github.com/rvben/vedetta/commit/bf30d1a8d81c1db663d90ae759516ac1cc1fa376))
- **api**: add htmx partial endpoints and wire recorder to server ([86faaad](https://github.com/rvben/vedetta/commit/86faaad374e20e842ae87145c9b0fc60c45c974e))
- **ui**: redesign web dashboard with Control Room Noir theme ([4ba7bd1](https://github.com/rvben/vedetta/commit/4ba7bd1c05a0f2447ccdc453657bb1ec5ff22994))
- **storage**: add data layer queries for UI dashboard ([4899028](https://github.com/rvben/vedetta/commit/4899028d2f78dde1ee63019f2f1db7f0981e64a6))
- complete Phase 1 core features ([3543309](https://github.com/rvben/vedetta/commit/35433091138a90efea3b79793fea8a28cca9b105))
- initial Watchpost scaffold ([8f01cf3](https://github.com/rvben/vedetta/commit/8f01cf3bdc6b9820fe087c4eab8dc4bac12c3e6f))

### Fixed

- **recording**: copy subscribers slice in disk monitor and guard zero interval ([396d89a](https://github.com/rvben/vedetta/commit/396d89ab02aaa40a3c0116d62ada2411afa779dd))
- **ui**: prefer pre-generated MP4 clip over HLS re-segmenter for event playback ([a1395c3](https://github.com/rvben/vedetta/commit/a1395c36301b288949ca392838d74894b9658f0a))
- **ui**: event page video playback uses .m3u8 and hls.js fallback ([9fe9e78](https://github.com/rvben/vedetta/commit/9fe9e78cf2606199cce4a281bb5887cabf6866be))
- **api**: serve event clip inline by default, attachment on ?download=1 ([a2767cf](https://github.com/rvben/vedetta/commit/a2767cf423e7bbe2dee6e2e245b4dc7157fc9c55))
- **ui**: iOS PWA navigation via sw postMessage ([5a011c2](https://github.com/rvben/vedetta/commit/5a011c2f9d8e44851598df651f79f88484950823))
- **notify**: pass raw email to webpush-go, not mailto: URI ([f80a8c0](https://github.com/rvben/vedetta/commit/f80a8c08e86c5b284e2844791a90b28a3c1aae2f))
- **notify**: capture push service response body on non-2xx ([9670dc9](https://github.com/rvben/vedetta/commit/9670dc94b676e780319b7fb964c27e07ba68fc97))
- **vedetta**: use real domain in VAPID subscriber claim ([feb9aac](https://github.com/rvben/vedetta/commit/feb9aac98b4cc09f3010ac7eec7b869bd94ec8aa))
- **storage**: dispatch fanout iterates push_subscriptions, not auth_users ([ad3e26a](https://github.com/rvben/vedetta/commit/ad3e26a0dfbdef4e87bec15761286867db1e8c0a))
- **storage**: drop FK from push_subscriptions to auth_users ([b2daabc](https://github.com/rvben/vedetta/commit/b2daabc572cbc7c67a6f786c49ed21396fe2a754))
- **api**: accept proxy-kind principal on push endpoints ([a46a152](https://github.com/rvben/vedetta/commit/a46a152a36cb4cd1a0f8411d23d792ef230a531b))
- **api**: allow anonymous access to PWA manifest, service worker, and icons ([d3215d7](https://github.com/rvben/vedetta/commit/d3215d7baa9e39ad38e6868ba0aa64d2da248d78))
- **notify**: separate 60s 429 backoff, log timeouts, guard nil vapid, per-sub context ([be33d58](https://github.com/rvben/vedetta/commit/be33d587c3e412165631e9bef4cdaac70baa1e19))
- **onvif**: attach RTSP packet handler after SetupAll ([6a8bd97](https://github.com/rvben/vedetta/commit/6a8bd9748c2e4da3705751f32c5309fd98bc1d98))
- **stream**: honor X-Forwarded-Proto in MSE origin check ([685755d](https://github.com/rvben/vedetta/commit/685755d65cc0bcfcca7eeb49180010306048cbf0))
- **media**: eliminate GC-unsafe C pointer aliasing in transcoder ([997f75d](https://github.com/rvben/vedetta/commit/997f75d44ba306ca7bca9ac6ba2f824452c3866f))
- **recompression**: reset stuck failure counters on startup ([ee77d19](https://github.com/rvben/vedetta/commit/ee77d19f10f9451b87ec9ebd5925c26157ebbe53))
- **retention**: clean orphan segments from removed cameras ([8278c7e](https://github.com/rvben/vedetta/commit/8278c7ee548224ae8fcf624373c5a0abb237e9b7))
- **detect**: handle NaN in fastSigmoid ([75ce3c5](https://github.com/rvben/vedetta/commit/75ce3c5564cd7001b0ff4ded049b6feeb36d40da))
- **ci**: move sgemmThreshold to darwin-only file ([20aeead](https://github.com/rvben/vedetta/commit/20aeeade727a8d398c7c20481c43b4c678dcf169))
- **ci**: upgrade to golangci-lint v2 and resolve all lint findings ([4a28cba](https://github.com/rvben/vedetta/commit/4a28cbaab37b63ef01507f98e1125c6da9a93e8a))
- resolve all golangci-lint v2 findings ([d634722](https://github.com/rvben/vedetta/commit/d63472211f4f121f6cc4cdc0e79125bc0a7d1c8c))
- **onvif**: probe RTSP with credentials when unauthenticated fails ([d80dbdc](https://github.com/rvben/vedetta/commit/d80dbdc4736a8c2a1d8536c0b99266cfb02382e2))
- **api**: use app context for camera start and disconnect RTSP on stop ([5969dda](https://github.com/rvben/vedetta/commit/5969ddad8eb5fda87c5e985051d47315df96aaa4))
- **recording**: recover from OpenH264 panics during recompression ([6a7d322](https://github.com/rvben/vedetta/commit/6a7d32248c8268e43d6c6f15436bc6226b55a37a))
- **recording**: fix flaky export cleanup when context is canceled ([5dbafab](https://github.com/rvben/vedetta/commit/5dbafabad9fe7ea93bf8fee174b2a3ae1c089bcf))
- **media**: fix transcoder failing to decode any frames ([f12d200](https://github.com/rvben/vedetta/commit/f12d2002a7a4f4b550c273f38c5a4600ea7ddffc))
- **media**: serialize OpenH264 C library calls to prevent memory corruption ([b8d3293](https://github.com/rvben/vedetta/commit/b8d32935a1f774c1d253fcfc4c871c68da326eb6))
- **recording**: prevent send on closed channel panic in RecordingConsumer ([262a3d2](https://github.com/rvben/vedetta/commit/262a3d22299a8ae2157b715a0486fb25c0cc1abd))
- **ui**: use 'Single sign-on' label instead of 'reverse proxy' ([301b029](https://github.com/rvben/vedetta/commit/301b0299721a4b70bb5039bb45b0df1ba7712c81))
- **api**: use application context for manual recompression trigger ([678c49b](https://github.com/rvben/vedetta/commit/678c49b2ddf14cbb7d64b54aeff3dfad2c13364e))
- **media**: compute encoder fps from actual sample duration, improve audio track test ([bda68c9](https://github.com/rvben/vedetta/commit/bda68c9ef5b46de7acc2308207e740e554ea6009))
- **storage**: preserve recompression state in SaveSegment upsert; add tests ([27afa37](https://github.com/rvben/vedetta/commit/27afa375af12656640fc464d6d2ef081b78c63ff))
- **playback**: preserve partial fragments from in-progress segments ([45b012c](https://github.com/rvben/vedetta/commit/45b012cdc662a5a343d7a39984dbaf16bdb95263))
- **playback**: skip unreadable segments in HLS playlist generation ([1d8b480](https://github.com/rvben/vedetta/commit/1d8b480b72ce45c04f31ff25cc9733e81f560684))
- **playback**: strip in-band SPS/PPS from HLS segments for Safari compat ([73dcf0e](https://github.com/rvben/vedetta/commit/73dcf0e5185f3963777c16e12b38887a221cbf3c))
- **playback**: serve init segment directly instead of byte-range ([b5d7a2e](https://github.com/rvben/vedetta/commit/b5d7a2eedeb83f196838d5705207f7cbaedb63b4))
- **playback**: split multi-track moofs into single-track for MSE compat ([9c7cb0f](https://github.com/rvben/vedetta/commit/9c7cb0fba9d74c9c5175b3aeb9eb8ab351b710cb))
- **ui**: cap waveform coverage at current time when viewing today ([2c513e3](https://github.com/rvben/vedetta/commit/2c513e3cd7b17e9db159ea6c2ce01365cd5e2291))
- **ui**: render waveform in local time instead of UTC ([c51b764](https://github.com/rvben/vedetta/commit/c51b764d4bf725b7a73875509d577e790f33dc5e))
- **ui**: remove duplicate track variable declaration in scrubTimeline ([7624eb5](https://github.com/rvben/vedetta/commit/7624eb56425c128471356273c8768dfc3ef54be1))
- **ui**: render waveform as distinct bars with gaps instead of continuous line ([ffe2217](https://github.com/rvben/vedetta/commit/ffe2217999ce217fe67bfdb70e73d31196d6583a))
- **ui**: show baseline waveform bars for recorded minutes without motion data ([adf7e2a](https://github.com/rvben/vedetta/commit/adf7e2a6f1bcd4e8e5abb210c9506f82fa55c466))
- **ui**: suppress auto-seek after resume to allow catch-up playback ([b854214](https://github.com/rvben/vedetta/commit/b854214630002dbd20fa7fb50d8035f72988f5ca))
- **ui**: disable auto-seek to live edge while paused ([206b8b8](https://github.com/rvben/vedetta/commit/206b8b8ded2ee1959d6ceb835d8a15446c29a1e8))
- **ui**: seek back to paused position on resume ([d007be6](https://github.com/rvben/vedetta/commit/d007be60111233a74009652da1a50fb5fb1cb56d))
- **ui**: live TV-style pause with seek-to-live button ([bd53429](https://github.com/rvben/vedetta/commit/bd534291923929e899440612acfe25a3fac4e99e))
- **ui**: pause freezes video frame instead of killing stream ([defecb9](https://github.com/rvben/vedetta/commit/defecb9c355bc295bec90bc5d4c14ab7d467e7fb))
- **ptz**: defer context cancel until after reading response body ([fe464c0](https://github.com/rvben/vedetta/commit/fe464c013579deb4cf1c591b85305aff0c28454f))
- **ptz**: try Tapo ONVIF port 2020 first, increase timeout ([2bbd65d](https://github.com/rvben/vedetta/commit/2bbd65dcebcb1c57183a43abe95dc78e0240ffca))
- **ptz**: fallback to WS-Security for GetProfiles ([a76b6e0](https://github.com/rvben/vedetta/commit/a76b6e0b021a3fb0949a456162936951798a27b3))
- **ptz**: probe multiple ONVIF ports for device discovery ([c787c50](https://github.com/rvben/vedetta/commit/c787c50323f8fda018f4bec9884f4c850affd6b2))
- **ui**: hide detection overlay when playing event video ([712d85a](https://github.com/rvben/vedetta/commit/712d85a936f3005396e2462e3baef7f4fa51c878))
- **onboarding**: redirect to login page after setup completes ([056fff1](https://github.com/rvben/vedetta/commit/056fff148a35d7ab1d7e78b50ed9bf49f4867c60))
- ignoring a face in the modal also deletes the face record ([6e394a0](https://github.com/rvben/vedetta/commit/6e394a06784eeaec1ffb790da811a20d7d62d37e))
- **ui**: filter unidentified appearances by score >= 0.65 to reduce false positives ([058e02e](https://github.com/rvben/vedetta/commit/058e02ec60da28f8cb79575afd38bdae0a895c7c))
- **ui**: unwrap events API response for unidentified appearances ([0584d5b](https://github.com/rvben/vedetta/commit/0584d5b4c51b45f6cacc97c99e4adc8b985692cb))
- **api**: include source_event_id in people list response for body-tracked persons ([fde63ef](https://github.com/rvben/vedetta/commit/fde63efa910c6a7e2ca03a305c438d34ce31e961))
- **ui**: preserve original case in 'New: <name>' identify chip ([47450d3](https://github.com/rvben/vedetta/commit/47450d388cc11fe1778a5a8749298f98d4425045))
- save clean snapshots to disk, use annotated only for MQTT display ([8debb57](https://github.com/rvben/vedetta/commit/8debb57d11d70f36f321b54ff2c7181bf876f59b))
- **ui**: show tracked status instead of tracking form on identified events ([6424caf](https://github.com/rvben/vedetta/commit/6424caf03e221c2e36557a534d3efb77dd79838d))
- **detect**: correct pixel indexing for SubImage crops in object embedder ([a4382bb](https://github.com/rvben/vedetta/commit/a4382bb5a2ed1a994322678b09cdeeef04cd963e))
- **ui**: add bottom nav to objects page and link references to source events ([40bf97a](https://github.com/rvben/vedetta/commit/40bf97a9b7ef5c8270102233783b515e94b6d1d1))
- **camera**: scale event bounding box to snapshot resolution ([b48ad7a](https://github.com/rvben/vedetta/commit/b48ad7abc8bd7312097dee3a04d2e8771e6971a0))
- **detect**: fix OSNet model compatibility and add embedding test ([7802f64](https://github.com/rvben/vedetta/commit/7802f64848deef787530e97b80216e0dcb6e7166))
- **face**: save unrecognized faces as unmatched instead of creating persons ([2b23c66](https://github.com/rvben/vedetta/commit/2b23c66de75bb57a382be850749f3b9485a995af))
- **face**: restore correct pixel offset in SCRFD input preprocessing ([f9dd2fa](https://github.com/rvben/vedetta/commit/f9dd2fae7138669bf96e36da109cd87ba45cbd6a))
- **ui**: remove motion state from camera status dot ([65e75bf](https://github.com/rvben/vedetta/commit/65e75bf485b091e88a9d0c4a0db2816876b3ac2e))
- **face**: correct MobileFaceNet download URL and startup race ([f1b6d97](https://github.com/rvben/vedetta/commit/f1b6d9707e23eb474949c31aa9558985637b1036))
- **rtsp-server**: re-packetize H264 RTP to fix oversized packet drops ([eba54e5](https://github.com/rvben/vedetta/commit/eba54e5b2955573f071ac43d18b559f4ed6b083f))
- **ui**: add mobile responsive styles for recordings page ([35b03f5](https://github.com/rvben/vedetta/commit/35b03f5c5ec025b09dade24ac3da5fdc7118220d))
- **ui**: play recording from segment directly instead of downloading clip ([be305d3](https://github.com/rvben/vedetta/commit/be305d3468a5cce35b3b46f4acad1dac482b1986))
- **ui**: show snapshot first on event page, play clip on demand ([88eb003](https://github.com/rvben/vedetta/commit/88eb003e0ea6d3102a16d2c31797bbfca0711c7c))
- **media**: use per-track timescales in fMP4 trim to prevent oversized clips ([0f49918](https://github.com/rvben/vedetta/commit/0f49918522f4bae666e6a119e950787f9b646441))
- **api**: return camera array from /api/cameras so birdseye grid works ([c779bf8](https://github.com/rvben/vedetta/commit/c779bf8376f4c4e6b8ad6a7c2227b6387e9726c5))
- **ui**: order cameras by config file instead of random map iteration ([a5c28ce](https://github.com/rvben/vedetta/commit/a5c28ce25be6a46224bad8f162534236624e4f11))
- **recording**: register segments in DB at creation and hide missing recordings ([e360c53](https://github.com/rvben/vedetta/commit/e360c53281801a98777514aa904be5ad6edbda58))
- **ui**: continuous recording playback across segment boundaries ([84c7375](https://github.com/rvben/vedetta/commit/84c7375182c897be00403d06e8202404305920cd))
- **events**: purge orphaned events with missing snapshot files on startup ([5d6d8cf](https://github.com/rvben/vedetta/commit/5d6d8cf9601be0d06d043ccee215255391d69f6a))
- **api**: fall back to camera snapshot when event snapshot file is missing ([3a30398](https://github.com/rvben/vedetta/commit/3a303981db3ac9b9ed608898974a52d8e96fd365))
- **api**: client-side timezone localization and export handler timeout ([b2b8454](https://github.com/rvben/vedetta/commit/b2b8454e4fdb8d10559a6c248d90f6b2fab994ef))
- **recording**: escalate ensureSegment errors instead of silently dropping them ([abfb52b](https://github.com/rvben/vedetta/commit/abfb52b4d8b0b08d1fabaae9d8545abc3a338c01))
- **recording**: add timeout to export segment processing ([d4a3e28](https://github.com/rvben/vedetta/commit/d4a3e2857d16e0aee4b1647cf7e2a56510a959f8))
- **api**: eliminate health endpoint blocking from SQLite contention and mutex deadlock ([cd860a9](https://github.com/rvben/vedetta/commit/cd860a928944f17ba492dc8dadc9ee016085fbfa))
- **playback**: server-side fMP4 trimming for accurate timeline scrubbing ([c290e25](https://github.com/rvben/vedetta/commit/c290e251fe57a29e91b6fd0ffa84297eab783911))
- **storage**: normalize timestamps to UTC for consistent SQLite comparisons ([d671193](https://github.com/rvben/vedetta/commit/d67119312471e1f78fa308ffd71fcc7c560c3929))
- **webrtc**: reliable live streaming with proper codec negotiation and IPv4 ([acc4c8e](https://github.com/rvben/vedetta/commit/acc4c8efcf3ef9af6da4ee6fc98d5ab5b20f6e81))
- **ui**: recordings page shows no recordings due to timezone and race conditions ([2bb0df6](https://github.com/rvben/vedetta/commit/2bb0df6b84e92ef5dc7937b348d1071c33c1e89b))
- **ui**: mobile bottom tabs not stretching to full width ([bcccb0f](https://github.com/rvben/vedetta/commit/bcccb0fc5fd18662e067a0946273700c8f4a1808))
- **events**: stop emitting track-end events ([a8ea774](https://github.com/rvben/vedetta/commit/a8ea774c22ddda1c5015c3c6fa3d953917ab6691))
- **snapshot**: async decode for main stream, remove debug logging ([db26880](https://github.com/rvben/vedetta/commit/db268804dc0152210d8c39f7e7ffea3392925614))
- **detect**: prepend SPS/PPS for Tapo camera H264 decode ([3681ed6](https://github.com/rvben/vedetta/commit/3681ed69561acdf7dfd6badd91819f67860c22a8))
- **recording**: harden shutdown, startup, and operational observability ([8299391](https://github.com/rvben/vedetta/commit/8299391d8f51193d17028d3bac5856076ebdce48))
- **recording**: graceful shutdown, startup clip race, and operational improvements ([9a3fb6d](https://github.com/rvben/vedetta/commit/9a3fb6de7c0acabe293f35ed538684c7351338c2))
- **camera**: base IsOnline on RTSP connection state instead of decoded frames ([3b6bb86](https://github.com/rvben/vedetta/commit/3b6bb86dd775eaf80aa73b18968e0bdbcf5d84d8))
- **rtsp**: strip credentials from URLs instead of masking them ([244f431](https://github.com/rvben/vedetta/commit/244f4317054ffb0cb79a0303e2d8f19d57bd17fd))
- **rtsp**: redact credentials from RTSP URLs in log output ([262704b](https://github.com/rvben/vedetta/commit/262704b1a8d9089ad0ea79c7133fb84cc81fee17))
- **rtsp**: use single OnPacketRTPAny handler instead of per-media loop ([c6b5440](https://github.com/rvben/vedetta/commit/c6b5440a423086216c15ad984e5666f8ba3447b5))
- **ui**: replace htmx polling with in-place snapshot and stats refresh ([f05e4c7](https://github.com/rvben/vedetta/commit/f05e4c79509161365d0ec23117560b810e7eb08a))
- **detect**: clamp sigmoid LUT index to prevent out-of-bounds panic ([942aa7e](https://github.com/rvben/vedetta/commit/942aa7eacd18da09364d8aa5052eb881fd2115ad))
- **ui**: cap live viewport height so timeline is visible without scrolling ([1cb9241](https://github.com/rvben/vedetta/commit/1cb924162243f8e2784a641c1d87b7b7fdce1193))
- **lint**: resolve all golangci-lint warnings and update .gitignore ([121c68b](https://github.com/rvben/vedetta/commit/121c68bf87d0d96906346b00380f279fd3b28a83))
- **config**: fix parseByteSize suffix ordering and add tests ([06ff8b3](https://github.com/rvben/vedetta/commit/06ff8b3ed2ce129f62a1aeaf5f531cfdf6e4f6f2))
- **api**: use event snapshots in gallery and fix recordings template ([ec63262](https://github.com/rvben/vedetta/commit/ec63262d0e090bb5791b3e42cb6a8fcbd0bf8830))
- **recording**: TOCTOU safety and single-pass clip extraction ([6f9f35a](https://github.com/rvben/vedetta/commit/6f9f35a8ef60df6e8a54b067c2d0970c83e96d67))
- **recording**: fix race condition, retention efficiency, and missing timeouts ([a434f14](https://github.com/rvben/vedetta/commit/a434f1413771fa3f7f4fb0bbfeaa52843a2397dc))
- **detect**: serialize concurrent Detect calls with mutex ([4d41e10](https://github.com/rvben/vedetta/commit/4d41e105bea38ac2a7d8c903cdcaaf9fb3cfbb98))
- **recording**: resolve three bugs preventing segment recording ([61431e0](https://github.com/rvben/vedetta/commit/61431e09118474a6b14ec5feb86c2ee2c1a008fa))

### Performance

- **media**: narrow OpenH264 mutex to create/destroy and encoder ops ([e8d2c19](https://github.com/rvben/vedetta/commit/e8d2c19546e0da3229e983b835163d433bf641a5))
- **recording**: move segment scanning to background goroutine ([6c143ce](https://github.com/rvben/vedetta/commit/6c143cec113da5dcc1528f7cda9c60c9f4c4f2f0))
- **snapshot**: optimize drawing and file I/O ([d428dda](https://github.com/rvben/vedetta/commit/d428ddafbf525f109b074ef4fd1ec778d0e93fdf))
- **stream**: eliminate per-frame allocations in MJPEG handler ([312a765](https://github.com/rvben/vedetta/commit/312a76547efc4c8c8bbc2f64f95d85c3a5df2e49))
- **detect**: eliminate per-frame allocations in motion detector ([2febf47](https://github.com/rvben/vedetta/commit/2febf479072e648b005465fd7f0bc0f85e1736b1))
- **camera**: eliminate per-frame RGBA allocation in frame pipeline ([6096041](https://github.com/rvben/vedetta/commit/60960419fbac9238b4b21c03a15e48d148cfa26d))
- **detect**: reuse preprocessing buffer across inference calls ([ce577e0](https://github.com/rvben/vedetta/commit/ce577e023bd9fffbd28a6d32cb090e0890695b9a))
- **detect**: optimize ONNX runtime from 97ms to 72ms per inference ([d45baf8](https://github.com/rvben/vedetta/commit/d45baf8ddd4fca1bd9dfe3841a6e5659db5c4caf))
- **detect**: optimize pure Go ONNX runtime from 533ms to 97ms per inference ([c9774c8](https://github.com/rvben/vedetta/commit/c9774c83e64ca99780778c7d9c5c02fe30366b46))
- **recording**: optimize continuous recording storage ([b3807a6](https://github.com/rvben/vedetta/commit/b3807a69c4cbd0048c9182b9e27e65b27b3285bb))
