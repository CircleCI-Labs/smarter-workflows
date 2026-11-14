"""Tasks of the worker."""


def normalize_name(name):
    """Return the name without surrounding blanks, or "world" when it is blank."""
    if not isinstance(name, str) or not name.strip():
        return "world"
    return name.strip()


def greetings(names):
    """Return one greeting per name, in the shape of libs/proto/schema.json."""
    out = []
    for name in names:
        who = normalize_name(name)
        out.append({"message": "Hello, %s!" % who, "name": who})
    return out
