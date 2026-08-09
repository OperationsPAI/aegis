from typing import Any

import neo4j
import networkx as nx

from ...clients.neo4j import get_neo4j_driver
from ...logging import logger, timeit


@timeit(log_args={"clear"})
def export_networkx_to_neo4j(graph: nx.MultiDiGraph, *, clear: bool = True, driver: neo4j.Driver | None = None) -> bool:
    needs_close = driver is None
    if driver is None:
        driver = get_neo4j_driver()

    try:
        driver.verify_connectivity()
    except Exception as e:
        logger.error(f"Failed to connect to Neo4j database: {e}")
        return False

    with driver.session() as session:
        # Optionally clear the database
        if clear:
            session.run("MATCH (n) DETACH DELETE n")
            logger.info("Cleared Neo4j database")

        for node_id, node_data in graph.nodes(data=True):
            assert isinstance(node_id, int)
            assert isinstance(node_data, dict)

            node_kind = node_data["kind"]
            assert isinstance(node_kind, str) and node_kind

            props: dict[str, Any] = {}
            for key, value in node_data.items():
                if isinstance(value, (str, int, float, bool)):
                    props[key] = value

            cypher = """
            MERGE (n:$($node_kind) {id: $id})
            SET n += $props
            """

            session.run(
                cypher,  # type:ignore
                node_kind=node_kind,
                id=str(node_id),
                props=props,
            )

        logger.opt(colors=True).info(f"Exported {graph.number_of_nodes()} nodes to Neo4j")

        # Create relationships
        for src_id, dst_id, edge_data in graph.edges(data=True):
            assert isinstance(src_id, int)
            assert isinstance(dst_id, int)
            assert isinstance(edge_data, dict)

            edge_kind = edge_data["kind"]
            assert isinstance(edge_kind, str) and edge_kind

            props: dict[str, Any] = {}
            for key, value in edge_data.items():
                if isinstance(value, (str, int, float, bool)):
                    props[key] = value

            cypher = """
            MATCH (src {id: $src_id}), (dst {id: $dst_id})
            MERGE (src)-[r:$($edge_kind)]->(dst)
            SET r += $props
            """
            session.run(
                cypher,  # type:ignore
                src_id=str(src_id),
                dst_id=str(dst_id),
                edge_kind=edge_kind,
                props=props,
            )

        logger.opt(colors=True).info(f"Exported {graph.number_of_edges()} edges to Neo4j")

    if needs_close:
        driver.close()
        logger.info("Closed Neo4j connection")

    logger.opt(colors=True).info("Exported SDG to Neo4j successfully")

    return True
