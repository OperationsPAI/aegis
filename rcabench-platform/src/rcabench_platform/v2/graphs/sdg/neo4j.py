import neo4j

from ...clients.neo4j import get_neo4j_driver
from ...logging import logger, timeit
from .defintion import SDG


@timeit(log_args={"clear"})
def export_sdg_to_neo4j(sdg: SDG, *, clear: bool = True, driver: neo4j.Driver | None = None) -> bool:
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

        for node in sdg.iter_nodes():
            node_kind = str(node.kind)

            props = {
                "name": node.self_name,
                "kind": node_kind,
                "self_name": node.self_name,
                "uniq_name": node.uniq_name,
            }

            if (created_by := node.data.get("created_by")) is not None:
                props["created_by"] = created_by

            cypher = """
            MERGE (n:$($node_kind) {id: $id})
            SET n += $props
            """

            session.run(
                cypher,  # type:ignore
                node_kind=node_kind,
                id=str(node.id),
                props=props,
            )

        logger.opt(colors=True).info(f"Exported {sdg.num_nodes()} nodes to Neo4j")

        # Create relationships
        for edge in sdg.iter_edges():
            edge_kind = str(edge.kind)

            props = {
                "kind": edge_kind,
            }

            cypher = """
            MATCH (src {id: $src_id}), (dst {id: $dst_id})
            MERGE (src)-[r:$($edge_kind)]->(dst)
            SET r += $props
            """
            session.run(
                cypher,  # type:ignore
                src_id=str(edge.src_id),
                dst_id=str(edge.dst_id),
                edge_kind=edge_kind,
                props=props,
            )

        logger.opt(colors=True).info(f"Exported {sdg.num_edges()} edges to Neo4j")

    if needs_close:
        driver.close()
        logger.info("Closed Neo4j connection")

    logger.opt(colors=True).info("Exported SDG to Neo4j successfully")

    return True
