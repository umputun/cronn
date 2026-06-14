Please do not tag commits unless all builders at
https://modern-c.appspot.com/-/builder/?importpath=modernc.org%2fsqlite are
happy.

Unlike most modernc.org repositories, this one is deliberately NOT
auto-tagged: builder.json sets "autotag": "<none>". Many projects depend on
modernc.org/sqlite, so letting the build bots tag it automatically is too
risky. Releases here are tagged manually by the maintainer, once all the
builders above are green.
