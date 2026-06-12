# Chaos-point catalog diff — current vs observed (trace-mined)

Surface-level (per-family capability variants collapsed). `+` = in observed only (gained), `-` = in current only (lost). JVM/DB families are current-only **by design** (not trace-derivable) and listed separately, not as losses.


## hs

| family | current | observed | +gained | -lost |
|---|---|---|---|---|
| workload | 9 | 9 | 0 | 0 |
| http | 0 | 11 | 11 | 0 |
| network | 8 | 8 | 0 | 0 |
| dns | 0 | 8 | 8 | 0 |

**HTTP** — +11 / -0
  - gained (11): attractions POST /attractions.Attractions/NearbyCinema; attractions POST /attractions.Attractions/NearbyMus; attractions POST /attractions.Attractions/NearbyRest; geo POST /geo.Geo/Nearby; profile POST /profile.Profile/GetProfiles; rate POST /rate.Rate/GetRates; recommendation POST /recommendation.Recommendation/GetRecommendations; reservation POST /reservation.Reservation/CheckAvailability; reservation POST /reservation.Reservation/MakeReservation; search POST /search.Search/Nearby; user POST /user.User/CheckUser

## media

| family | current | observed | +gained | -lost |
|---|---|---|---|---|
| workload | 14 | 13 | 0 | 1 |
| http | 8 | 21 | 18 | 5 |
| network | 7 | 17 | 15 | 5 |
| dns | 6 | 17 | 15 | 4 |

**HTTP** — +18 / -5
  - lost (5): nginx-web-server POST /wrk2-api/cast-info/write; nginx-web-server POST /wrk2-api/movie-info/write; nginx-web-server POST /wrk2-api/movie/register; nginx-web-server POST /wrk2-api/plot/write; nginx-web-server POST /wrk2-api/user/register
  - gained (18): cast-info-service POST /CastInfoService/ReadCastInfo; cast-info-service POST /PageService/ReadPage; compose-review-service POST /ComposeReviewService/UploadMovieId; compose-review-service POST /ComposeReviewService/UploadRating; compose-review-service POST /ComposeReviewService/UploadText; compose-review-service POST /ComposeReviewService/UploadUniqueId; compose-review-service POST /ComposeReviewService/UploadUserId; movie-id-service POST /MovieIdService/UploadMovieId; movie-info-service POST /MovieInfoService/ReadMovieInfo; movie-review-service POST /MovieReviewService/ReadMovieReviews; movie-review-service POST /MovieReviewService/UploadMovieReview; plot-service POST /PlotService/ReadPlot  …

**NETWORK** — +15 / -5
  - lost (5): cast-info-service page-service; nginx-web-server compose-review-service; nginx-web-server page-service; nginx-web-server plot-service; nginx-web-server user-service
  - gained (15): cast-info-service movie-info-service; cast-info-service movie-review-service; cast-info-service plot-service; compose-review-service movie-review-service; compose-review-service review-storage-service; compose-review-service user-review-service; movie-id-service compose-review-service; movie-id-service rating-service; movie-review-service review-storage-service; nginx-web-server movie-id-service; nginx-web-server text-service; nginx-web-server unique-id-service  …

**WORKLOAD(services)** — +0 / -1
  - lost (1): page-service

## otel-demo

| family | current | observed | +gained | -lost |
|---|---|---|---|---|
| workload | 18 | 17 | 1 | 2 |
| http | 3 | 47 | 44 | 0 |
| network | 24 | 21 | 4 | 7 |
| dns | 10 | 21 | 18 | 7 |

_current also has 128 jvm/db points (by design not emitted by observed)._

**HTTP** — +44 / -0
  - gained (44): ad POST /oteldemo.AdService/GetAds; cart POST /oteldemo.CartService/AddItem; cart POST /oteldemo.CartService/EmptyCart; cart POST /oteldemo.CartService/GetCart; checkout POST /oteldemo.CheckoutService/PlaceOrder; currency POST /oteldemo.CurrencyService/Convert; email POST /send_order_confirmation; flagd POST /flagd.evaluation.v1.Service/ResolveBoolean; flagd POST /flagd.evaluation.v1.Service/ResolveFloat; flagd POST /flagd.evaluation.v1.Service/ResolveInt; flagd POST /ofrep/v1/evaluate/flags/loadGeneratorFloodHomepage; frontend GET /  …

**NETWORK** — +4 / -7
  - lost (7): accounting postgresql; cart valkey-cart; email checkout; frontend load-generator; image-provider load-generator; shipping checkout; shipping load-generator
  - gained (4): frontend-proxy frontend; load-generator flagd; load-generator frontend-proxy; payment flagd

**WORKLOAD(services)** — +1 / -2
  - lost (2): postgresql; valkey-cart
  - gained (1): frontend-proxy

## sn

| family | current | observed | +gained | -lost |
|---|---|---|---|---|
| workload | 9 | 12 | 3 | 0 |
| http | 6 | 11 | 5 | 0 |
| network | 5 | 2 | 0 | 3 |
| dns | 5 | 2 | 0 | 3 |

**HTTP** — +5 / -0
  - gained (5): post-storage-service POST /PostStorageService/ReadPosts; social-graph-service POST /SocialGraphService/FollowWithUsername; url-shorten-service POST /UrlShortenService/ComposeUrls; user-mention-service POST /UserMentionService/ComposeUserMentions; user-service POST /UserService/RegisterUserWithId

**NETWORK** — +0 / -3
  - lost (3): nginx-thrift compose-post-service; nginx-thrift home-timeline-service; nginx-thrift user-timeline-service

**WORKLOAD(services)** — +3 / -0
  - gained (3): media-service; text-service; unique-id-service

## sockshop

| family | current | observed | +gained | -lost |
|---|---|---|---|---|
| workload | 7 | 7 | 0 | 0 |
| http | 26 | 44 | 29 | 11 |
| network | 11 | 8 | 5 | 8 |
| dns | 11 | 8 | 5 | 8 |

_current also has 3662 jvm/db points (by design not emitted by observed)._

**HTTP** — +29 / -11
  - lost (11): catalog GET /catalogue*; catalog GET /catalogue/images*; front-end DELETE /carts/*; front-end GET /carts/*/items; front-end GET /carts/*/merge; front-end GET /catalogue; front-end GET /catalogue/*; front-end GET /customers/*; front-end GET /customers/*/addresses; front-end GET /customers/*/cards; front-end POST /carts/*/items
  - gained (29): carts DELETE /carts/*; carts DELETE /carts/user0; carts GET /; carts GET /carts/*/items; carts GET /carts/*/merge; carts GET /carts/user0/items; carts GET /carts/user0/merge; carts POST /carts/*/items; carts POST /carts/user0/items; catalog GET /; front-end POST /register; orders GET /  …

**NETWORK** — +5 / -8
  - lost (8): carts front-end; catalog front-end; front-end catalogue; orders front-end; orders user; payment front-end; shipping front-end; users front-end
  - gained (5): front-end catalog; front-end users; orders payment; orders shipping; orders users

## teastore

| family | current | observed | +gained | -lost |
|---|---|---|---|---|
| workload | 6 | 6 | 0 | 0 |
| http | 34 | 25 | 10 | 19 |
| network | 6 | 5 | 0 | 1 |
| dns | 6 | 5 | 0 | 1 |

_current also has 3269 jvm/db points (by design not emitted by observed)._

**HTTP** — +10 / -19
  - lost (19): teastore-auth POST /tools.descartes.teastore.auth/rest/cart/add/*; teastore-auth PUT /tools.descartes.teastore.registry/rest/services/tools.descartes.teastore.auth/teastore-auth-0.teastore-auth; teastore-image PUT /tools.descartes.teastore.registry/rest/services/tools.descartes.teastore.image/teastore-image-0.teastore-image; teastore-persistence GET /tools.descartes.teastore.persistence/rest/categories/*; teastore-persistence GET /tools.descartes.teastore.persistence/rest/products/*; teastore-persistence GET /tools.descartes.teastore.persistence/rest/products/category/*; teastore-persistence GET /tools.descartes.teastore.persistence/rest/products/count/*; teastore-persistence GET /tools.descartes.teastore.persistence/rest/users/name/*; teastore-persistence PUT /tools.descartes.teastore.registry/rest/services/tools.descartes.teastore.persistence/teastore-persistence-0.teastore-persistence; teastore-recommender GET /tools.descartes.teastore.registry/rest/services/tools.descartes.teastore.persistence/; teastore-recommender GET /tools.descartes.teastore.registry/rest/services/tools.descartes.teastore.recommender/; teastore-recommender PUT /tools.descartes.teastore.registry/rest/services/tools.descartes.teastore.recommender/teastore-recommender-0.teastore-recommender  …
  - gained (10): teastore-auth POST /tools.descartes.teastore.auth/rest/cart/add/{pid}; teastore-persistence GET /tools.descartes.teastore.persistence/rest/categories/{id:[0-9][0-9]*}; teastore-persistence GET /tools.descartes.teastore.persistence/rest/products/category/{category:[0-9][0-9]*}; teastore-persistence GET /tools.descartes.teastore.persistence/rest/products/count/{category:[0-9][0-9]*}; teastore-persistence GET /tools.descartes.teastore.persistence/rest/products/{id:[0-9][0-9]*}; teastore-persistence GET /tools.descartes.teastore.persistence/rest/users/name/{name}; teastore-persistence GET /tools.descartes.teastore.registry/rest/services/tools.descartes.teastore.persistence/; teastore-registry GET /rest/services/{name}; teastore-registry PUT /rest/services/{name}/{location}; teastore-webui GET /*

**NETWORK** — +0 / -1
  - lost (1): teastore-registry teastore-webui

## ts

| family | current | observed | +gained | -lost |
|---|---|---|---|---|
| workload | 43 | 40 | 0 | 3 |
| http | 162 | 199 | 151 | 114 |
| network | 115 | 85 | 2 | 32 |
| dns | 115 | 85 | 2 | 32 |

_current also has 9610 jvm/db points (by design not emitted by observed)._

**HTTP** — +151 / -114
  - lost (114): ts-admin-basic-info-service GET /api/v1/configservice/configs; ts-admin-basic-info-service GET /api/v1/contactservice/contacts; ts-admin-basic-info-service GET /api/v1/priceservice/prices; ts-admin-basic-info-service GET /api/v1/stationservice/stations; ts-admin-basic-info-service GET /api/v1/trainservice/trains; ts-admin-basic-info-service POST /api/v1/configservice/configs; ts-admin-basic-info-service POST /api/v1/contactservice/contacts/*; ts-admin-basic-info-service POST /api/v1/stationservice/stations; ts-admin-basic-info-service POST /api/v1/trainservice/trains; ts-admin-basic-info-service PUT /api/v1/configservice/configs; ts-admin-basic-info-service PUT /api/v1/contactservice/contacts; ts-admin-basic-info-service PUT /api/v1/stationservice/stations  …
  - gained (151): ts-admin-basic-info-service GET /api/v1/adminbasicservice/adminbasic/configs; ts-admin-basic-info-service GET /api/v1/adminbasicservice/adminbasic/contacts; ts-admin-basic-info-service GET /api/v1/adminbasicservice/adminbasic/prices; ts-admin-basic-info-service GET /api/v1/adminbasicservice/adminbasic/stations; ts-admin-basic-info-service GET /api/v1/adminbasicservice/adminbasic/trains; ts-admin-basic-info-service POST /api/v1/adminbasicservice/adminbasic/configs; ts-admin-basic-info-service POST /api/v1/adminbasicservice/adminbasic/contacts; ts-admin-basic-info-service POST /api/v1/adminbasicservice/adminbasic/stations; ts-admin-basic-info-service POST /api/v1/adminbasicservice/adminbasic/trains; ts-admin-basic-info-service PUT /api/v1/adminbasicservice/adminbasic/configs; ts-admin-basic-info-service PUT /api/v1/adminbasicservice/adminbasic/contacts; ts-admin-basic-info-service PUT /api/v1/adminbasicservice/adminbasic/stations  …

**NETWORK** — +2 / -32
  - lost (32): loadgenerator ts-rabbitmq; ts-assurance-service mysql; ts-auth-service mysql; ts-config-service mysql; ts-consign-price-service mysql; ts-consign-service mysql; ts-contacts-service mysql; ts-delivery-service mysql; ts-delivery-service ts-rabbitmq; ts-food-delivery-service mysql; ts-food-service mysql; ts-food-service ts-rabbitmq  …
  - gained (2): loadgenerator ts-ui-dashboard; ts-food-service ts-delivery-service

**WORKLOAD(services)** — +0 / -3
  - lost (3): mysql; ts-rabbitmq; ts-rebook-service

## Totals (trace-derived families)

| family | total +gained | total -lost |
|---|---|---|
| workload | 4 | 6 |
| http | 268 | 149 |
| network | 26 | 56 |
| dns | 48 | 55 |
