import os

import neo4j


def get_neo4j_driver(
    uri: str | None = None,
    username: str | None = None,
    password: str | None = None,
) -> neo4j.Driver:
    """
    Establish a connection to Neo4j database

    Args:
        uri: Neo4j database URI, defaults to environment variable NEO4J_URI or "bolt://localhost:7687"
        username: Neo4j username, defaults to environment variable NEO4J_USERNAME or "neo4j"
        password: Neo4j password, defaults to environment variable NEO4J_PASSWORD

    Returns:
        Neo4j driver instance
    """
    # Use parameters or fallback to environment variables or defaults
    uri = uri or os.environ.get("NEO4J_URI", "bolt://localhost:7687")
    username = username or os.environ.get("NEO4J_USERNAME", "neo4j")
    password = password or os.environ.get("NEO4J_PASSWORD", "your_password")

    driver = neo4j.GraphDatabase.driver(uri, auth=(username, password))
    return driver
