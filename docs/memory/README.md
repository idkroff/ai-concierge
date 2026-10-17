# Фича «Память»: документация

Память о пользователе и его бронях для Telegram-бота AI Concierge. Проектный семинар, 4 курс, ФКН НИУ ВШЭ. Автор: Федор Крупнов, БПМИ238.

| Документ | Что внутри |
|---|---|
| [requirements/1.1_OnePager.pdf](requirements/1.1_OnePager.pdf) | Одностраничник: проблема, аудитория, решение, цель пилота |
| [requirements/2.1_PRD.md](requirements/2.1_PRD.md) | PRD: бизнес-цели BG-1…BG-4, метрики M-1, P-1, P-2, G-1…G-4, scope, non-goals, риски |
| [requirements/2.2_SRS.md](requirements/2.2_SRS.md) | SRS: FR-01…FR-22, NFR-01…NFR-09 с критериями приёмки |
| [requirements/2.3_UserStories.md](requirements/2.3_UserStories.md) | User Stories US-01…US-17 с критериями приёмки Given/When/Then |
| [requirements/2.4_RTM.xlsx](requirements/2.4_RTM.xlsx) | Матрица трассировки PRD → US → SRS → тест-кейсы TC-01…TC-38; собирается скриптом [requirements/build_rtm.py](requirements/build_rtm.py), который сверяет все ID между документами |
| [research.md](research.md) | Исследование: подходы к памяти LLM-агентов, Yandex AI Studio, YDB, ограничения Telegram, аналоги, выводы для дизайна |
| [design.md](design.md) | Технический дизайн: архитектура, модель данных YDB, потоки, контракты, события для метрик, трассировка на SRS, план следующего этапа |
| [diagrams/](diagrams/README.md) | Диаграммы: краткий флоу «как работает память», C4 контейнеры и компоненты, ER-схема YDB; исходники Graphviz и Mermaid рядом |

Файлы `.docx` в `requirements/` сгенерированы из `.md` через pandoc; исходник правды — markdown.
