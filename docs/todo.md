* flesh out projects more, ability to share projects, add notes to projects, add reference materials, url bookmarks, user comments
* for items like ICs / electronic components we should have a automated datasheet fetcher
* the projects reference urls should include something that can try to archive the page, even if its just a headless chrome making a pdf or full page screenshot, we don't want to be archiving something like youtube videos though. should be opt in per url.
* AI integration for faster adding, we can configure an AI service to use in a config, the AI can look at the image and autofill the fields for adding new items
* items should have a product page field, also archivable

the headless chrome can be used for the datasheet fetcher as well if we need to go that route, many sites dont like bots.. also this could be a good candidate to use camofox instead of headless chrome its designed to bypass bot detections.

~~we will want more install instructions on how to set this up in deployment on alpine/debian/arch (prod runs on alpine right now)~~ done 2026-10-22: docs/INSTALL.md


~~* item page sharing visibility can be a toggle on the page instead of inside edit page~~ done 2026-10-22
~~* item delete should explain that "Log usage" is probably what they want instead~~ done 2026-10-22
* should Log usage be named something else?
~~* adding new items, maybe this can be streamlined for the location, like maybe add item from the locations page fills that in~~ done 2026-10-22 ("Add item" per location row prefills the form; "save and add another" variant not done)
~~* moving items: from the items list we should be able to select multiple items to be able to change the location on~~ done 2026-10-22: checkbox selection + select-all-on-page + bulk-move endpoint (200/call, 0 clears)
~~* search should do partial matches maybe use that trigraph thing idr what its called~~ done 2026-10-22 (FTS5 trigram tokenizer; needs ≥3 chars)
~~* location adding should have be more streamlined for adding a location with its sublocations like a parts organizer with many compartments, this should include like add sublocations X,Y,Z (each dimension optional) just for helping name the added sublocations they might want to do a x,y like A1 B3 or like row 1 col 2 or this could be getting too complicated for the naming part.. also in this same vien of parts organizers we should be able to copy/reference the layout of one location to add another.~~ done 2026-10-22: grid generator (flat/nested × compact/verbose naming, optional prefix, live preview) + copy-layout endpoint, picker in the add form AND per-row "Duplicate layout…"

~~* include a systemd user service file and a openrc script and instructions on how to use each~~ done 2026-10-22: scripts/systemd/, scripts/openrc/
* include a docker build or something? i have no idea how to deploy docker but people will probably appreciate it
~~* on desktop hover over a item thumb can open a larger version in a tooltip like popup~~ done 2026-10-22
* deleting a location with sublocations should have the option to delete all sublocations
* ~~add ability to add a picture of locations, same thumbnail behaviour as items, but we only need the 1 pic not multiple~~ done 2026-10-23
