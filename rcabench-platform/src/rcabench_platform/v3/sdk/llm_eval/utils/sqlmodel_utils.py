from sqlmodel import Session, create_engine, text

from .env import EnvUtils
from .log import get_logger

logger = get_logger(__name__)


class SQLModelUtils:
    _engine = None  # singleton
    _db_available = None  # cache for database availability check
    _last_check_time = None  # timestamp of last check

    @classmethod
    def get_engine(cls):
        if cls._engine is None:
            db_url = EnvUtils.get_env("LLM_EVAL_DB_URL", None) or EnvUtils.get_env("UTU_DB_URL")
            if not db_url:
                raise ValueError("LLM_EVAL_DB_URL or UTU_DB_URL environment variable is not set")
            cls._engine = create_engine(
                db_url,
                pool_size=10,
                max_overflow=20,
                pool_timeout=30,
                pool_pre_ping=True,
            )
            # Ensure DB schema/tables exist on first engine init
            try:
                cls._init_db_schema(cls._engine)
            except Exception as e:
                logger.warning(f"Auto schema creation skipped due to error: {e}")
        return cls._engine

    @staticmethod
    def create_session():
        return Session(SQLModelUtils.get_engine(), expire_on_commit=False)

    @classmethod
    def check_db_available(cls, force_check: bool = False, cache_ttl: int = 60) -> bool:
        """Check if database is available with caching.

        Args:
            force_check: Force a fresh check, bypassing cache
            cache_ttl: Cache time-to-live in seconds (default: 60s)

        Returns:
            bool: True if database is available, False otherwise
        """
        import time

        # Return cached result if available and not expired
        if not force_check and cls._db_available is not None and cls._last_check_time is not None:
            if time.time() - cls._last_check_time < cache_ttl:
                logger.debug(f"Using cached DB availability status: {cls._db_available}")
                return cls._db_available

        # Perform actual check
        logger.debug("Performing fresh database availability check")

        db_url = EnvUtils.get_env("LLM_EVAL_DB_URL", None) or EnvUtils.get_env("UTU_DB_URL", "")
        if not db_url:
            cls._db_available = False
            cls._last_check_time = time.time()
            return False

        try:
            engine = cls.get_engine()
            with engine.connect() as conn:
                conn.execute(text("SELECT 1"))
            cls._db_available = True
            cls._last_check_time = time.time()
            logger.debug("Database is available")
            return True
        except Exception as e:
            cls._db_available = False
            cls._last_check_time = time.time()
            logger.error(f"Database connection failed: {e}")
            return False

    @staticmethod
    def _init_db_schema(engine):
        """
        Import all SQLModel table definitions and create tables if they do not exist.
        Also handles schema migrations for existing tables.
        """
        # Import models so SQLModel knows about all tables
        try:
            # Core models registered here
            from ..db import eval_datapoint  # noqa: F401
        except Exception as e:
            logger.debug(f"Model import warning (non-fatal): {e}")

        # Create tables if not exist
        try:
            from sqlmodel import SQLModel

            SQLModel.metadata.create_all(engine)
            logger.info("Database schema ensured (tables created if missing).")
        except Exception as e:
            logger.warning(f"SQLModel metadata create_all failed: {e}")

        # Run schema migrations for existing tables
        try:
            SQLModelUtils._run_migrations(engine)
        except Exception as e:
            logger.warning(f"Schema migration failed: {e}")

    @staticmethod
    def _run_migrations(engine):
        """
        Run schema migrations to add new columns to existing tables.
        This ensures backward compatibility with existing databases.
        """
        migrations = [
            # Add tags column to data table
            ("data", "tags", "JSON"),
            # Add tags column to evaluation_data table
            ("evaluation_data", "tags", "JSON"),
        ]

        # Index migrations: (index_name, create_sql)
        _t = "evaluation_data"
        index_migrations = [
            ("ix_eval_exp_id", f"CREATE INDEX IF NOT EXISTS ix_eval_exp_id ON {_t} (exp_id)"),
            ("ix_eval_stage", f"CREATE INDEX IF NOT EXISTS ix_eval_stage ON {_t} (stage)"),
            ("ix_eval_exp_stage", f"CREATE INDEX IF NOT EXISTS ix_eval_exp_stage ON {_t} (exp_id, stage)"),
        ]

        with engine.connect() as conn:
            for table_name, column_name, column_type in migrations:
                if not SQLModelUtils._column_exists(conn, table_name, column_name):
                    try:
                        conn.execute(text(f"ALTER TABLE {table_name} ADD COLUMN {column_name} {column_type}"))
                        conn.commit()
                        logger.info(f"Migration: Added column '{column_name}' to table '{table_name}'")
                    except Exception as e:
                        logger.debug(f"Migration skipped for {table_name}.{column_name}: {e}")

            # Create indexes if they don't exist
            for index_name, create_sql in index_migrations:
                try:
                    conn.execute(text(create_sql))
                    conn.commit()
                    logger.info(f"Migration: Ensured index '{index_name}'")
                except Exception as e:
                    logger.debug(f"Index migration skipped for {index_name}: {e}")

    @staticmethod
    def _column_exists(conn, table_name: str, column_name: str) -> bool:
        """Check if a column exists in a table (supports SQLite and PostgreSQL)."""
        db_url = EnvUtils.get_env("LLM_EVAL_DB_URL", None) or EnvUtils.get_env("UTU_DB_URL", "") or ""

        if db_url.startswith("sqlite"):
            # SQLite: use PRAGMA
            try:
                result = conn.execute(text(f"PRAGMA table_info({table_name})"))
                columns = [row[1] for row in result.fetchall()]
                return column_name in columns
            except Exception:
                return False
        else:
            # PostgreSQL and other databases: use information_schema
            try:
                result = conn.execute(
                    text(
                        "SELECT column_name FROM information_schema.columns "
                        "WHERE table_name = :table_name AND column_name = :column_name"
                    ),
                    {"table_name": table_name, "column_name": column_name},
                )
                return result.fetchone() is not None
            except Exception:
                # Fallback: try to select the column
                try:
                    conn.execute(text(f"SELECT {column_name} FROM {table_name} LIMIT 0"))
                    return True
                except Exception:
                    return False
